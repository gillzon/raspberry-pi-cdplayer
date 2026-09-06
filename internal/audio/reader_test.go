package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRealReaderSeeksCDImageWithoutReopening(t *testing.T) {
	if err := Check(); err != nil {
		t.Skipf("optional libcdio integration: %v", err)
	}
	dir := t.TempDir()
	pcm := make([]byte, 44100*4*4)
	for i := 0; i < 44100*4; i++ {
		sample := uint16(int16(8000 * math.Sin(2*math.Pi*440*float64(i)/44100)))
		binary.LittleEndian.PutUint16(pcm[i*4:], sample)
		binary.LittleEndian.PutUint16(pcm[i*4+2:], sample)
	}
	if err := os.WriteFile(filepath.Join(dir, "audio.bin"), pcm, 0600); err != nil {
		t.Fatal(err)
	}
	cue := filepath.Join(dir, "audio.cue")
	if err := os.WriteFile(cue, []byte("FILE \"audio.bin\" BINARY\n TRACK 01 AUDIO\n  INDEX 01 00:00:00\n TRACK 02 AUDIO\n  INDEX 01 00:02:00\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bad, err := openReader(ctx, cue, []Layout{{1, 0, 151}, {2, 150, 300}})
	if err == nil {
		bad.Close()
		t.Fatal("accepted a changed disc layout")
	}
	if !strings.Contains(err.Error(), "expected 0..151, reader reported 0..150") {
		t.Fatalf("missing track boundary diagnostic: %v", err)
	}
	for _, speed := range []int{0, 4} {
		for _, verify := range []bool{false, true} {
			r, err := ReaderWithOptions(verify, speed)(ctx, cue, []Layout{{1, 0, 150}, {2, 150, 300}})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			for _, request := range []readCall{{0, 75}, {75, 75}, {150, 75}, {2, 1}, {225, 75}} {
				var got []byte
				for len(got) < request.count*sectorBytes {
					part, err := r.Read(request.sector+len(got)/sectorBytes, request.count-len(got)/sectorBytes)
					if err != nil {
						t.Fatal(err)
					}
					got = append(got, part...)
				}
				if !bytes.Equal(got, pcm[request.sector*sectorBytes:(request.sector+request.count)*sectorBytes]) {
					t.Fatalf("incorrect PCM at sector %d, verify=%v", request.sector, verify)
				}
			}
			r.Close()
		}
	}
}
func TestCacheStorageLockAndCrashCleanup(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "cd-audio-obsolete")
	if err := os.Mkdir(old, 0700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(root, "unrelated")
	os.WriteFile(keep, []byte("keep"), 0600)
	first := &Cache{Root: root}
	if err := first.Init(); err != nil {
		t.Fatal(err)
	}
	defer first.Shutdown()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("obsolete session retained")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("unrelated file removed")
	}
	second := &Cache{Root: root}
	if err := second.Init(); err == nil {
		second.Shutdown()
		t.Fatal("two processes may share cache")
	}
}

func TestStartupErrorIncludesHelperDiagnostic(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	_, err := openReaderProgram(context.Background(), "/dev/fake", nil, "import sys; print('CD reader: permission denied opening drive',file=sys.stderr); sys.exit(7)")
	if err == nil || !strings.Contains(err.Error(), "permission denied opening drive") || !strings.Contains(err.Error(), "exit status 7") {
		t.Fatalf("lost helper failure: %v", err)
	}
}
func TestStartupSignalIsNotHiddenByCancellation(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	_, err := openReaderProgram(context.Background(), "/dev/fake", nil, "import os,signal; os.kill(os.getpid(),signal.SIGTERM)")
	if err == nil || !strings.Contains(err.Error(), "signal: terminated") {
		t.Fatalf("lost process signal: %v", err)
	}
}
func TestDiagnosticTailBounded(t *testing.T) {
	d := &diagnosticTail{}
	d.Write(bytes.Repeat([]byte("x"), 10000))
	d.Write([]byte(" final error"))
	if len(d.String()) > 8192 || !strings.HasSuffix(d.String(), "final error") {
		t.Fatal("diagnostic tail lost error or exceeded limit")
	}
}

func TestReadFailureReportsCause(t *testing.T) {
	for _, tc := range []struct {
		name, program, want string
		timeout             time.Duration
	}{
		{"exit", "print('CD reader: drive read failed',file=sys.stderr);sys.exit(9)", "exit status 9", time.Second},
		{"signal", "import os,signal;os.kill(os.getpid(),signal.SIGTERM)", "signal: terminated", time.Second},
		{"timeout", "import time;time.sleep(10)", "CD read timed out after 20ms", 20 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := "import sys\nprint('CDPCM1',flush=True)\nsys.stdin.readline()\n" + tc.program
			r, err := openReaderProgram(context.Background(), "/dev/fake", nil, program)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			r.(*processReader).readTimeout = tc.timeout
			_, err = r.Read(150, 1)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "sectors 150..151") {
				t.Fatalf("lost read failure: %v", err)
			}
		})
	}
}

func TestReaderSurvivesSpawningThreadExit(t *testing.T) {
	// A locked goroutine that returns causes its OS thread to be destroyed.
	// The helper must remain alive while the Go process is alive.
	prefix := strings.Split(readerProgram, "\ndef main():")[0]
	program := prefix + "\nwatch_parent(int(sys.argv[3]))\nprint('CDPCM1',flush=True)\nfor line in sys.stdin:\n sys.stdout.buffer.write(struct.pack('<I',2352)+bytes(2352))\n sys.stdout.buffer.flush()\n"
	result := make(chan Reader, 1)
	failures := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer close(done)
		r, err := openReaderProgram(context.Background(), "/dev/fake", nil, program)
		if err != nil {
			failures <- err
			return
		}
		result <- r
	}()
	<-done
	select {
	case err := <-failures:
		t.Fatal(err)
	case r := <-result:
		defer r.Close()
		time.Sleep(600 * time.Millisecond)
		if _, err := r.Read(0, 1); err != nil {
			t.Fatalf("reader died with spawning thread: %v", err)
		}
	}
}
