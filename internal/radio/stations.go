// Package radio contains the built-in Swedish live radio presets.
package radio

type Station struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"-"`
}

// Direct broadcaster streams; never accept arbitrary URLs from control requests.
var stations = []Station{
	{"p1", "P1", "https://live1.sr.se/p1-aac-128"},
	{"p2", "P2", "https://live1.sr.se/p2-aac-128"},
	{"p3", "P3", "https://live1.sr.se/p3-aac-128"},
	{"p4-stockholm", "P4 Stockholm", "https://www.sverigesradio.se/topsy/direkt/srapi/701.mp3"},
	{"rockklassiker", "Rockklassiker", "https://tx-bauerse.sharp-stream.com/http_live.php?i=rockklassiker_instream_se_mp3"},
	{"rix-fm", "RIX FM", "https://fm01-ice.stream.khz.se/fm01_mp3"},
	{"nrj", "Energy (NRJ)", "https://live-bauerse-fm.sharp-stream.com/nrj_instreamtest_se_mp3?direct=true"},
}

func Stations() []Station { return append([]Station(nil), stations...) }
func Find(id string) (Station, bool) {
	for _, station := range stations {
		if station.ID == id {
			return station, true
		}
	}
	return Station{}, false
}
func Next(id string, delta int) Station {
	for i, station := range stations {
		if station.ID == id {
			return stations[(i+delta%len(stations)+len(stations))%len(stations)]
		}
	}
	return stations[0]
}
