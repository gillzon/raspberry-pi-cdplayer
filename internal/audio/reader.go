package audio

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

//go:embed reader.py
var readerProgram string

type Layout struct {
	Number int `json:"number"`
	Start  int `json:"start"`
	End    int `json:"end"`
}
type Reader interface {
	Read(int, int) ([]byte, error)
	Close() error
}
type OpenReader func(context.Context, string, []Layout) (Reader, error)
type processReader struct {
	cmd         *exec.Cmd
	in          io.WriteCloser
	out         *bufio.Reader
	cancel      context.CancelFunc
	diagnostics *diagnosticTail
	waitOnce    sync.Once
	waitErr     error
}

func openReader(ctx context.Context, device string, layout []Layout) (Reader, error) {
	return openReaderProgram(ctx, device, layout, readerProgram)
}

func openReaderProgram(ctx context.Context, device string, layout []Layout, program string) (Reader, error) {
	ctx, cancel := context.WithCancel(ctx)
	payload, _ := json.Marshal(layout)
	cmd := exec.CommandContext(ctx, "python3", "-u", "-c", program, device, string(payload))
	diagnostics := &diagnosticTail{}
	cmd.Stderr = io.MultiWriter(os.Stderr, diagnostics)
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		cancel()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		input.Close()
		cancel()
		return nil, err
	}
	r := &processReader{cmd: cmd, in: input, out: bufio.NewReader(output), cancel: cancel, diagnostics: diagnostics}
	// A stalled device must not retain a helper indefinitely during startup.
	timer := time.AfterFunc(30*time.Second, cancel)
	greeting, err := r.out.ReadString('\n')
	if err != nil || greeting != "CDPCM1\n" {
		// EOF normally means the helper exited. Reap it before cancelling so the
		// actual exit status/signal and all stderr output survive into the UI.
		if !errors.Is(err, io.EOF) {
			cancel()
		}
		exitErr := r.wait()
		timer.Stop()
		contextErr := ctx.Err()
		r.in.Close()
		cancel()
		reason := diagnostics.String()
		if reason == "" {
			reason = "helper exited without diagnostic output"
		}
		if contextErr != nil {
			return nil, fmt.Errorf("open persistent CD reader: startup cancelled or exceeded 30s: %v; %s", contextErr, reason)
		}
		if exitErr != nil {
			return nil, fmt.Errorf("open persistent CD reader: %v; %s", exitErr, reason)
		}
		return nil, fmt.Errorf("open persistent CD reader: invalid greeting %q (%v); %s", greeting, err, reason)
	}
	timer.Stop()
	return r, nil
}
func (r *processReader) Read(sector, count int) ([]byte, error) {
	timer := time.AfterFunc(30*time.Second, r.cancel)
	defer timer.Stop()
	if _, err := fmt.Fprintf(r.in, "%d %d\n", sector, count); err != nil {
		return nil, err
	}
	var size uint32
	if err := binary.Read(r.out, binary.LittleEndian, &size); err != nil {
		return nil, err
	}
	if size == 0 || size > uint32(count*2352) || size%2352 != 0 {
		return nil, fmt.Errorf("invalid CD reader response size %d", size)
	}
	data := make([]byte, size)
	_, err := io.ReadFull(r.out, data)
	return data, err
}
func (r *processReader) wait() error {
	r.waitOnce.Do(func() { r.waitErr = r.cmd.Wait() })
	return r.waitErr
}
func (r *processReader) Close() error { r.cancel(); r.in.Close(); return r.wait() }

// Keep diagnostics bounded even if a faulty helper produces excessive output.
type diagnosticTail struct {
	mu   sync.Mutex
	data []byte
}

func (d *diagnosticTail) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(p)
	const limit = 8192
	if len(p) >= limit {
		d.data = append(d.data[:0], p[len(p)-limit:]...)
	} else {
		if len(d.data)+len(p) > limit {
			d.data = d.data[len(d.data)+len(p)-limit:]
		}
		d.data = append(d.data, p...)
	}
	return n, nil
}
func (d *diagnosticTail) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.TrimSpace(string(d.data))
}

// Check validates the installed runtime without opening a drive.
func Check() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "python3", "-c", readerProgram, "--check").CombinedOutput()
	if err != nil {
		return fmt.Errorf("persistent CD reader unavailable (install python3 and cd-paranoia): %w: %s", err, output)
	}
	return nil
}
