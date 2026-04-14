package main

import (
	"encoding/json/jsontext"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"sync"

	"github.com/izokina/contractor/pkg/external"
	"github.com/izokina/contractor/pkg/pipeline/contract"
	"github.com/izokina/contractor/pkg/pipeline/merge"
	"github.com/izokina/contractor/pkg/pipeline/parse"
)

func runPipeline(in io.Reader, out io.Writer, poolSize int) {
	parser := parse.NewParser()
	cin, errFunc := parser.ParseJson(jsontext.NewDecoder(in))
	merger := merge.NewMerger()

	wg := &sync.WaitGroup{}
	for range poolSize {
		wg.Go(func() {
			contractor := contract.NewContractor()
			for term := range cin {
				term = contractor.ContractAndNormalize(term)
				err := merger.Add(term)
				if err != nil {
					log.Fatal(err)
				}
			}
		})
	}

	err := errFunc()
	if err != nil {
		log.Fatal(err)
	}

	wg.Wait()

	if err := external.Dump(merger.Flush(), out); err != nil {
		log.Fatalf("Error during encoding: %v", err)
	}
}

func main() {
	threads := flag.Int("threads", runtime.NumCPU(), "Number of worker threads, defaults to CPU number")

	flag.Usage = func() {
		fmt.Printf("High-performance tensor index contractor.\n")
		fmt.Printf("\n")
		fmt.Printf("Reads ExpressionJSON from STDIN and writes the result to STDOUT.\n")
		fmt.Printf("See README.md for more details.\n")
		fmt.Printf("\n")
		fmt.Printf("Usage:\n")
		fmt.Printf("  contractor <flags>\n")
		fmt.Printf("\n")
		fmt.Printf("Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	runPipeline(os.Stdin, os.Stdout, *threads)
}
