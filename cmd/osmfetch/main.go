// Comando osmfetch: descarga de OpenStreetMap (API Overpass) los datos crudos de
// Lima Metropolitana y Callao: límites distritales, puntos con dirección
// (addr:street + addr:housenumber) y calles con nombre por distrito.
//
//	go run ./cmd/osmfetch -out data/osm
//
// Los archivos crudos no se versionan (se regeneran) y están bajo licencia ODbL:
// todo dato derivado debe conservar la atribución "© OpenStreetMap contributors".
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// areas son las provincias de la cobertura inicial (Lima Metropolitana y Callao).
const areas = `(area["ISO3166-2"="PE-LMA"];area["ISO3166-2"="PE-CAL"];)->.a;`

const (
	qDistricts = `[out:json][timeout:300];` + areas +
		`rel(area.a)["admin_level"="8"]["boundary"="administrative"]["pe:ubigeo"];out geom;`
	qAddresses = `[out:json][timeout:300];` + areas +
		`nwr(area.a)["addr:street"]["addr:housenumber"];out tags center;`
	// La relación de OSM r se consulta como el área 3600000000 + r.
	qStreets = `[out:json][timeout:300];area(%d)->.d;way(area.d)["highway"]["name"];out tags geom;`
)

func main() {
	out := flag.String("out", "data/osm", "directorio de salida")
	endpoint := flag.String("endpoint", "https://overpass-api.de/api/interpreter", "URL de la API Overpass")
	streets := flag.Bool("streets", true, "descargar también las calles por distrito")
	force := flag.Bool("force", false, "volver a descargar archivos que ya existen")
	flag.Parse()

	c := &client{endpoint: *endpoint, http: &http.Client{Timeout: 6 * time.Minute}}
	if err := os.MkdirAll(filepath.Join(*out, "streets"), 0o755); err != nil {
		log.Fatal(err)
	}

	districtsPath := filepath.Join(*out, "districts.json")
	for path, q := range map[string]string{districtsPath: qDistricts, filepath.Join(*out, "addresses.json"): qAddresses} {
		if err := c.fetchTo(path, q, *force); err != nil {
			log.Fatal(err)
		}
	}
	if !*streets {
		return
	}

	ids, err := districtRelations(districtsPath)
	if err != nil {
		log.Fatalf("distritos: %v", err)
	}
	// Un distrito que falla no detiene a los demás. Como los archivos existentes se
	// omiten, basta con volver a correr el comando para completar los que faltan.
	var failed []string
	for _, d := range ids {
		if err := c.fetchTo(filepath.Join(*out, "streets", d.ubigeo+".json"), fmt.Sprintf(qStreets, 3600000000+d.id), *force); err != nil {
			log.Print(err)
			failed = append(failed, d.ubigeo)
		}
	}
	if len(failed) > 0 {
		log.Fatalf("faltan las calles de %d distritos: %s; vuelve a correr el comando", len(failed), strings.Join(failed, ", "))
	}
	log.Printf("listo: %d distritos en %s", len(ids), *out)
}

type client struct {
	endpoint string
	http     *http.Client
}

// fetchTo ejecuta una consulta Overpass y guarda la respuesta. Reintenta ante
// límites de uso (429) y errores temporales del servidor.
func (c *client) fetchTo(path, query string, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		log.Printf("ya existe, se omite: %s", path)
		return nil
	}
	var lastErr error
	for attempt := 1; attempt <= 5; attempt++ {
		body, retry, err := c.query(query)
		if err == nil {
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, body, 0o644); err != nil {
				return err
			}
			if err := os.Rename(tmp, path); err != nil {
				return err
			}
			log.Printf("%s (%d KB)", path, len(body)/1024)
			return nil
		}
		lastErr = err
		if !retry {
			break
		}
		wait := time.Duration(attempt*30) * time.Second
		log.Printf("%s: %v; reintento en %s", path, err, wait)
		time.Sleep(wait)
	}
	return fmt.Errorf("%s: %w", path, lastErr)
}

func (c *client) query(q string) (body []byte, retry bool, err error) {
	req, err := http.NewRequest(http.MethodPost, c.endpoint, strings.NewReader(url.Values{"data": {q}}.Encode()))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "addrsvc-osmfetch/0.1")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("HTTP %d", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("HTTP %d: %.200s", resp.StatusCode, body)
	}
	// Overpass responde 200 con un "remark" cuando la consulta se corta por tiempo
	// o memoria: el resultado vendría incompleto.
	var probe struct {
		Remark string `json:"remark"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, false, fmt.Errorf("respuesta no es JSON: %w", err)
	}
	if probe.Remark != "" {
		return nil, true, fmt.Errorf("respuesta incompleta: %s", probe.Remark)
	}
	return body, false, nil
}

type relation struct {
	id     int64
	ubigeo string
}

func districtRelations(path string) ([]relation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Elements []struct {
			ID   int64             `json:"id"`
			Tags map[string]string `json:"tags"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	var out []relation
	for _, e := range f.Elements {
		out = append(out, relation{id: e.ID, ubigeo: e.Tags["pe:ubigeo"]})
	}
	return out, nil
}
