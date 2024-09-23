package main

import "encoding/json"

type Station struct {
	ID   json.Number `json:"id"`
	Name string      `json:"name"`
}
