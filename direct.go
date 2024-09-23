package main

import "encoding/json"

type Direct struct {
	ID    json.Number `json:"id"`
	Train Train       `json:"train"`
}
