package ncache

import "testing"

func FuzzNCacheParse(f *testing.F) {
	f.Add("2026-10-15 06:23:20.825 INFO [thread-1] Cache.Manager: started")
	f.Add("2026-10-15T06:23:20,825 ERROR")
	f.Add("2026-10-15 06:2")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		for _, p := range []Parser{{}, {name: EventsName, lay: eventLogLayout}} {
			rec, _ := p.Parse(s)
			for _, fl := range rec.Fields {
				_ = fl.Key + fl.Value
			}
		}
		layoutFromSample([]byte(s), cacheLogLayout)
	})
}
