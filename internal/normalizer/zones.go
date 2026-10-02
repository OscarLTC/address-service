package normalizer

import (
	"encoding/json"
	"os"
)

// LoadZones lee data/config/zones.json y devuelve qué zonas están activas.
func LoadZones(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Zones map[string]string `json:"zones"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	active := map[string]bool{}
	for zone, status := range f.Zones {
		active[zone] = status == "active"
	}
	return active, nil
}
