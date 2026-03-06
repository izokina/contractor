package external

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/izokina/contractor/pkg/literal"
)

func ReadExpression(reader io.Reader) <-chan any {
	cin := make(chan any)
	go func() {
		defer close(cin)

		decoder := json.NewDecoder(reader)
		decoder.UseNumber()

		for _, t := range []json.Token{json.Delim('['), literal.Plus} {
			if token, err := decoder.Token(); err != nil || token != t {
				if err == nil {
					err = fmt.Errorf("Unexpected token '%s'", token)
				}
				cin <- err
				return
			}
		}

		for decoder.More() {
			var item any
			if err := decoder.Decode(&item); err != nil {
				cin <- err
				return
			}
			cin <- item
		}

		if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
			if err == nil {
				err = fmt.Errorf("Unexpected token '%s'", token)
			}
			cin <- err
			return
		}
	}()
	return cin
}

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
