package audio

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	cancel context.CancelFunc
}

func openReader(ctx context.Context, device string, layout []Layout) (Reader, error) {
	ctx, cancel := context.WithCancel(ctx)
	payload, _ := json.Marshal(layout)
	cmd := exec.CommandContext(ctx, "python3", "-u", "-c", readerProgram, device, string(payload))
	cmd.Stderr = os.Stderr
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
	r := &processReader{cmd: cmd, in: input, out: bufio.NewReader(output), cancel: cancel}
	// A stalled device must not retain a helper indefinitely during startup.
	timer := time.AfterFunc(30*time.Second, cancel)
	greeting, err := r.out.ReadString('\n')
	timer.Stop()
	if err != nil || greeting != "CDPCM1\n" {
		r.Close()
		return nil, fmt.Errorf("open persistent CD reader: %q: %v", greeting, err)
	}
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
func (r *processReader) Close() error { r.cancel(); r.in.Close(); return r.cmd.Wait() }

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
