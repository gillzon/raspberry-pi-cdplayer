package mpd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Output struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
	Device  string `json:"device,omitempty"`
}

var outputDevice = regexp.MustCompile(`^CD Player: (.+) \[(plughw:CARD=[A-Za-z0-9_-]+,DEV=[0-9]+)\]$`)

func OutputDevice(name string) string {
	if m := outputDevice.FindStringSubmatch(name); m != nil {
		return m[2]
	}
	return ""
}
func (c *Client) Outputs() ([]Output, error) {
	outputs := []Output{}
	_, err := c.commandValues("outputs", func(k, v string) {
		if k == "outputid" {
			outputs = append(outputs, Output{ID: v})
			return
		}
		if len(outputs) == 0 {
			return
		}
		o := &outputs[len(outputs)-1]
		switch k {
		case "outputname":
			o.Name = v
			o.Label = v
			o.Device = OutputDevice(v)
			if m := outputDevice.FindStringSubmatch(v); m != nil {
				o.Label = m[1]
			}
		case "outputenabled":
			o.Enabled = v == "1"
		}
	})
	return outputs, err
}

// SelectOutput uses stable names; MPD output IDs may change after a restart.
func (c *Client) SelectOutput(name string) error {
	outputs, err := c.Outputs()
	if err != nil {
		return err
	}
	var selected *Output
	for i := range outputs {
		if outputs[i].Name == name {
			if selected != nil {
				return fmt.Errorf("duplicate output name")
			}
			selected = &outputs[i]
		}
	}
	if selected == nil {
		return fmt.Errorf("output is no longer configured; refresh Settings")
	}
	for _, o := range outputs {
		if !regexp.MustCompile(`^[0-9]+$`).MatchString(o.ID) {
			return fmt.Errorf("invalid MPD output ID")
		}
	}
	// Release the old device first: two configured outputs can refer to
	// the same exclusive ALSA hardware. Restore prior enable states on error.
	restore := func(cause error) error {
		for _, o := range outputs {
			action := "disableoutput "
			if o.Enabled {
				action = "enableoutput "
			}
			if _, e := c.command(action + o.ID); e != nil {
				return fmt.Errorf("output switch failed: %v; restoring outputs failed: %w", cause, e)
			}
		}
		return cause
	}
	for _, o := range outputs {
		if o.ID != selected.ID && o.Enabled {
			if _, err = c.command("disableoutput " + o.ID); err != nil {
				return restore(err)
			}
		}
	}
	if !selected.Enabled {
		if _, err = c.command("enableoutput " + selected.ID); err != nil {
			return restore(err)
		}
	}
	return nil
}
func LoadOutput(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}
func SaveOutput(path, name string) error {
	if path == "" {
		return fmt.Errorf("set a cache directory to save audio settings")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".output-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(name + "\n"); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
