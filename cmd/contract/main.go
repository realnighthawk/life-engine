package main

import (
	"encoding/json"
	"os"

	"github.com/realnighthawk/life-engine/internal/engine"
)

func main() {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(engine.New(nil).OpenAPI()); err != nil {
		panic(err)
	}
}
