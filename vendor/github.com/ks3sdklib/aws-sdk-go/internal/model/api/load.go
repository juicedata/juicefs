package api

import (
	"encoding/json"
	"fmt"
	"os"
)

// Load takes a set of files for each filetype and returns an API pointer.
// The API will be initialized once all files have been loaded and parsed.
//
// Will panic if any failure opening the definition JSON files, or there
// are unrecognized exported names.
func Load(api, docs, paginators, waiters string) *API {
	a := API{}
	a.Attach(api)
	a.Attach(docs)
	a.Attach(paginators)
	a.Attach(waiters)
	return &a
}

// Attach opens a file by name, and unmarshal its JSON data.
// Will proceed to setup the API if not already done so.
func (a *API) Attach(filename string) {
	f, err := os.Open(filename)
	defer f.Close()
	if err != nil {
		panic(err)
	}
	json.NewDecoder(f).Decode(a)

	if !a.initialized {
		a.Setup()
	}
}

// AttachString will unmarshal a raw JSON string, and setup the
// API if not already done so.
func (a *API) AttachString(str string) {
	json.Unmarshal([]byte(str), a)

	if !a.initialized {
		a.Setup()
	}
}

// Setup initializes the API.
func (a *API) Setup() {
	a.unrecognizedNames = map[string]string{}
	a.writeShapeNames()
	a.resolveReferences()
	a.fixStutterNames()
	a.renameExportable()
	a.renameToplevelShapes()
	a.updateTopLevelShapeReferences()
	a.createInputOutputShapes()
	a.customizationPasses()

	if !a.NoRemoveUnusedShapes {
		a.removeUnusedShapes()
	}

	if len(a.unrecognizedNames) > 0 {
		msg := []string{
			"Unrecognized inflections for the following export names:",
			"(Add these to inflections.csv with any inflections added after the ':')",
		}
		fmt.Fprintf(os.Stderr, "%s\n%s\n\n", msg[0], msg[1])
		for n, m := range a.unrecognizedNames {
			if n == m {
				m = ""
			}
			fmt.Fprintf(os.Stderr, "%s:%s\n", n, m)
		}
		os.Stderr.WriteString("\n\n")
		panic("Found unrecognized exported names in API " + a.PackageName())
	}

	a.initialized = true
}
