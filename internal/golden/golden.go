// Package golden lee y escribe el dataset de oro en CSV (formato de
// goldenset/template.csv). Lo comparten el generador y el evaluador.
package golden

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
)

// Header son las columnas del CSV, en orden.
var Header = []string{
	"id", "source", "raw_address", "district_field", "province_field", "department_field",
	"expected_street_type", "expected_street_name", "expected_number", "expected_block",
	"expected_lot", "expected_urbanization", "expected_ubigeo", "lat_verified", "lng_verified",
	"verification_method", "verified_by", "split", "notes",
}

// Row es una dirección del dataset con su parseo esperado.
type Row struct {
	ID                   string
	Source               string
	RawAddress           string
	DistrictField        string
	ProvinceField        string
	DepartmentField      string
	ExpectedStreetType   string
	ExpectedStreetName   string
	ExpectedNumber       string
	ExpectedBlock        string
	ExpectedLot          string
	ExpectedUrbanization string
	ExpectedUbigeo       string
	Lat                  string
	Lng                  string
	VerificationMethod   string
	VerifiedBy           string
	Split                string
	Notes                string
}

func (r Row) fields() []string {
	return []string{
		r.ID, r.Source, r.RawAddress, r.DistrictField, r.ProvinceField, r.DepartmentField,
		r.ExpectedStreetType, r.ExpectedStreetName, r.ExpectedNumber, r.ExpectedBlock,
		r.ExpectedLot, r.ExpectedUrbanization, r.ExpectedUbigeo, r.Lat, r.Lng,
		r.VerificationMethod, r.VerifiedBy, r.Split, r.Notes,
	}
}

func fromFields(f []string) Row {
	return Row{
		ID: f[0], Source: f[1], RawAddress: f[2], DistrictField: f[3], ProvinceField: f[4],
		DepartmentField: f[5], ExpectedStreetType: f[6], ExpectedStreetName: f[7],
		ExpectedNumber: f[8], ExpectedBlock: f[9], ExpectedLot: f[10],
		ExpectedUrbanization: f[11], ExpectedUbigeo: f[12], Lat: f[13], Lng: f[14],
		VerificationMethod: f[15], VerifiedBy: f[16], Split: f[17], Notes: f[18],
	}
}

// Read lee un CSV del dataset y valida la cabecera.
func Read(r io.Reader) ([]Row, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = len(Header)
	head, err := cr.Read()
	if err != nil {
		return nil, err
	}
	for i, h := range Header {
		if head[i] != h {
			return nil, fmt.Errorf("columna %d: se esperaba %q, hay %q", i+1, h, head[i])
		}
	}
	var rows []Row
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return nil, err
		}
		rows = append(rows, fromFields(rec))
	}
}

// ReadFile lee un CSV del dataset desde disco.
func ReadFile(path string) ([]Row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Read(f)
}

// Write escribe las filas con su cabecera.
func Write(w io.Writer, rows []Row) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(Header); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write(r.fields()); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
