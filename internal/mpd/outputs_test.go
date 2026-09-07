package mpd

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const hdmi = "CD Player: HDMI 1 [plughw:CARD=vc4hdmi0,DEV=0]"
const outputReply = "outputid: 7\noutputname: Old output\noutputenabled: 1\noutputid: 12\noutputname: " + hdmi + "\noutputenabled: 0\nOK\n"

func TestOutputSelectionAndRestore(t *testing.T) {
	for _, fail := range []bool{false, true} {
		c, done := serve(t, func(cmd string) string {
			if cmd == "outputs" {
				return outputReply
			}
			if fail && cmd == "enableoutput 12" {
				return "ACK [5@0] {enableoutput} unavailable\n"
			}
			return "OK\n"
		})
		if _, err := c.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		outputs, err := c.Outputs()
		if err != nil {
			t.Fatal(err)
		}
		if len(outputs) != 2 || outputs[1].Label != "HDMI 1" || outputs[1].Device != "plughw:CARD=vc4hdmi0,DEV=0" {
			t.Fatalf("%+v", outputs)
		}
		err = c.SelectOutput(hdmi)
		if (err != nil) != fail {
			t.Fatalf("failure=%v, err=%v", fail, err)
		}
		c.Close()
		want := []string{"outputs", "outputs", "disableoutput 7", "enableoutput 12"}
		if fail {
			want = append(want, "enableoutput 7", "disableoutput 12")
		}
		if got := <-done; !reflect.DeepEqual(got, want) {
			t.Fatalf("%v != %v", got, want)
		}
	}
}
func TestUnknownOutputDoesNotChangePlayback(t *testing.T) {
	c, done := serve(t, func(string) string { return outputReply })
	c.Connect(context.Background())
	if c.SelectOutput("missing\nstop") == nil {
		t.Fatal("accepted unknown output")
	}
	c.Close()
	if got := <-done; !reflect.DeepEqual(got, []string{"outputs"}) {
		t.Fatal(got)
	}
}
func TestOutputPreferencePersistsByName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings", "audio-output")
	if name, err := LoadOutput(path); name != "" || err != nil {
		t.Fatal(name, err)
	}
	if err := SaveOutput(path, hdmi); err != nil {
		t.Fatal(err)
	}
	name, err := LoadOutput(path)
	if err != nil || name != hdmi {
		t.Fatal(name, err)
	}
	c, done := serve(t, func(cmd string) string {
		if cmd == "outputs" {
			return strings.ReplaceAll(outputReply, "12", "23")
		}
		return "OK\n"
	})
	c.PreferredOutput = name
	if _, err = c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Close()
	if got := <-done; !reflect.DeepEqual(got, []string{"outputs", "disableoutput 7", "enableoutput 23"}) {
		t.Fatal(got)
	}
}
