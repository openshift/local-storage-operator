package assets

import (
	"embed"
	"strings"
)

//go:embed *
var f embed.FS

// ReadFile reads and returns the content of the named file.
func ReadFile(name string) ([]byte, error) {
	return f.ReadFile(name)
}

// ReadFileAndReplace reads the file, replaces a set of strings, and
// returns the resulting contents as a byte array. The pairs should be
// provided as key/value pairs using an array of strings, i.e.:
//	pairs := []string{
//		"${KEY_1}", "val_1",
//		"${KEY_2}", "val_2",
//	}
func ReadFileAndReplace(name string, pairs []string) ([]byte, error) {
	fileBytes, err := f.ReadFile(name)
	if err != nil {
		return nil, err
	}
	policyReplacer := strings.NewReplacer(pairs...)
	transformedString := policyReplacer.Replace(string(fileBytes))
	return []byte(transformedString), nil
}
