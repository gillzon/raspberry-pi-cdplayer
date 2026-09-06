package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
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
	r, err := openReader(ctx, cue, []Layout{{1, 0, 150}, {2, 150, 300}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, sector := range []int{0, 1, 150, 2} {
		got, err := r.Read(sector, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pcm[sector*sectorBytes:(sector+1)*sectorBytes]) {
			t.Fatalf("incorrect PCM at sector %d", sector)
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
