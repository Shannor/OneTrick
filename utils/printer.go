package utils

import (
	"encoding/json"
	"fmt"
)

// PrettyPrint pretty-prints a struct or map in JSON format.
func PrettyPrint(input interface{}) {
	bytes, err := json.MarshalIndent(input, "", "    ")
	if err != nil {
	}
	fmt.Println(string(bytes))
}
