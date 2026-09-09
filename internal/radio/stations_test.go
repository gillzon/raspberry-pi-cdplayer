package radio

import "testing"

func TestStationNavigationWrapsAndCatalogCannotBeMutated(t *testing.T) {
	list := Stations()
	if Next(list[0].ID, -1) != list[len(list)-1] || Next(list[len(list)-1].ID, 1) != list[0] {
		t.Fatal("station navigation does not wrap")
	}
	for _, station := range list {
		if found, ok := Find(station.ID); !ok || found != station {
			t.Fatalf("missing station %s", station.ID)
		}
	}
	list[0].Name = "changed"
	if Stations()[0].Name == "changed" {
		t.Fatal("catalog was mutated")
	}
	if _, ok := Find("https://example.com/arbitrary"); ok {
		t.Fatal("arbitrary URL accepted")
	}
}
