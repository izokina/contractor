package external

import (
	"encoding/json"
	"io"

	"github.com/izokina/contractor/pkg/literal"
)

func Dump(cout <-chan any, out io.Writer) error {
	var items []any
	for item := range cout {
		items = append(items, item)
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "\t")
	switch len(items) {
	case 0:
		return encoder.Encode(0)
	case 1:
		return encoder.Encode(items[0])
	default:
		return encoder.Encode(append([]any{literal.Plus}, items...))
	}
}
