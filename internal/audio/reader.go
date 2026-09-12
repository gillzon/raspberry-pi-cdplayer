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
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed reader.py
var readerProgram string

// DefaultReadSpeed keeps the drive quiet with headroom above real-time playback.
const DefaultReadSpeed = 2

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
	ctx         context.Context
	cancelCause context.CancelCauseFunc
	readTimeout time.Duration
	diagnostics *diagnosticTail
	waitOnce    sync.Once
	waitErr     error
}

func openReader(ctx context.Context, device string, layout []Layout) (Reader, error) {
	return ReaderWithOptions(false, DefaultReadSpeed)(ctx, device, layout)
}

// ReaderWithVerification enables extra overlapping reads and software repair.
// The default reader favours playback latency over extraction verification.
func ReaderWithVerification(verify bool) OpenReader {
	return ReaderWithOptions(verify, DefaultReadSpeed)
}

// ReaderWithOptions optionally requests a CD read-speed multiplier. This is
// not a current limit or a motor acceleration control; firmware may reject it.
func ReaderWithOptions(verify bool, speed int) OpenReader {
	return func(ctx context.Context, device string, layout []Layout) (Reader, error) {
		if speed < 0 || int64(speed) > 2147483647 {
			return nil, fmt.Errorf("invalid CD read speed %d", speed)
		}
		verification := "False"
		if verify {
			verification = "True"
		}
		program := fmt.Sprintf("VERIFY_AUDIO = %s\nCD_READ_SPEED = %d\n", verification, speed) + readerProgram
		return openReaderProgram(ctx, device, layout, program)
	}
}

func openReaderProgram(ctx context.Context, device string, layout []Layout, program string) (Reader, error) {
	ctx, cancelCause := context.WithCancelCause(ctx)
	cancel := func() { cancelCause(context.Canceled) }
	payload, _ := json.Marshal(layout)
	cmd := exec.CommandContext(ctx, "python3", "-u", "-c", program, device, string(payload), strconv.Itoa(os.Getpid()))
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
	r := &processReader{cmd: cmd, in: input, out: bufio.NewReader(output), cancel: cancel, ctx: ctx, cancelCause: cancelCause, readTimeout: 30 * time.Second, diagnostics: diagnostics}
	// A stalled device must not retain a helper indefinitely during startup.
	timer := time.AfterFunc(30*time.Second, func() { cancelCause(fmt.Errorf("reader startup timed out after 30s")) })
	greeting, err := r.out.ReadString('\n')
	if err != nil || greeting != "CDPCM1\n" {
		// EOF normally means the helper exited. Reap it before cancelling so the
		// actual exit status/signal and all stderr output survive into the UI.
		if !errors.Is(err, io.EOF) {
			cancel()
		}
		exitErr := r.wait()
		timer.Stop()
		contextErr := context.Cause(ctx)
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
	timer := time.AfterFunc(r.readTimeout, func() { r.cancelCause(fmt.Errorf("CD read timed out after %s", r.readTimeout)) })
	defer timer.Stop()
	if _, err := fmt.Fprintf(r.in, "%d %d\n", sector, count); err != nil {
		return nil, r.readFailure(sector, count, err)
	}
	var size uint32
	if err := binary.Read(r.out, binary.LittleEndian, &size); err != nil {
		return nil, r.readFailure(sector, count, err)
	}
	if size == 0 || size > uint32(count*2352) || size%2352 != 0 {
		r.cancel()
		return nil, r.readFailure(sector, count, fmt.Errorf("invalid CD reader response size %d", size))
	}
	data := make([]byte, size)
	_, err := io.ReadFull(r.out, data)
	if err != nil {
		return nil, r.readFailure(sector, count, err)
	}
	return data, err
}

func (r *processReader) readFailure(sector, count int, err error) error {
	// Let an exited helper report its real status before cancelling it.
	// The read timer remains active if the process is stuck after closing stdout.
	r.in.Close()
	exitErr := r.wait()
	cause := context.Cause(r.ctx)
	r.cancel()
	detail := r.diagnostics.String()
	if detail == "" {
		detail = "helper produced no diagnostic output"
	}
	return fmt.Errorf("read CD sectors %d..%d: %w; helper exit=%v; cancellation=%v; %s", sector, sector+count, err, exitErr, cause, detail)
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
