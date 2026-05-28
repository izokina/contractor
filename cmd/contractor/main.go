package main

import (
	"encoding/json/jsontext"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"

	"github.com/izokina/contractor/pkg/pipeline/codec"
	"github.com/izokina/contractor/pkg/pipeline/contract"
	"github.com/izokina/contractor/pkg/pipeline/eval"
	"github.com/izokina/contractor/pkg/pipeline/expr"
	"github.com/izokina/contractor/pkg/pipeline/merge"
	"github.com/izokina/contractor/pkg/pipeline/walk"
)

const Version = "0.1.0"

func runPipeline(in io.Reader, out io.Writer, poolSize int) {
	parser := codec.NewParser()
	merger := merge.NewMerger()

	cParse := make(chan expr.Expr)
	var parseErr error
	go func() {
		defer close(cParse)
		parseErr = parser.ParseJson(jsontext.NewDecoder(in), func(n expr.Expr) { cParse <- n })
	}()

	cTerms := make(chan expr.Term)
	go func() {
		defer close(cTerms)
		w := walk.NewWalker(eval.NewTermFolder(func(t expr.Term) { cTerms <- t }))
		for e := range cParse {
			w.Walk(e)
		}
	}()

	wg := &sync.WaitGroup{}
	for range poolSize {
		wg.Go(func() {
			contractor := contract.NewContractor()
			for term := range cTerms {
				merger.Add(contractor.ContractAndNormalize(term))
			}
		})
	}

	wg.Wait()
	if parseErr != nil {
		log.Fatal(parseErr)
	}

	enc := jsontext.NewEncoder(out, jsontext.WithIndent("\t"))
	if err := merger.Flush(enc); err != nil {
		log.Fatalf("Error during encoding: %v", err)
	}
}

func main() {
	threads := flag.Int("threads", runtime.NumCPU(), "Number of worker threads, defaults to CPU number")
	cpuProfile := flag.String("cpuprofile", "", "Write CPU profile to file")
	memProfile := flag.String("memprofile", "", "Write memory profile to file")
	blockProfile := flag.String("blockprofile", "", "Write block profile to file")
	mutexProfile := flag.String("mutexprofile", "", "Write mutex profile to file")
	version := flag.Bool("version", false, "Print version and exit")

	flag.Usage = func() {
		fmt.Printf("contractor %s - High-performance tensor index contractor.\n", Version)
		fmt.Printf("\n")
		fmt.Printf("Contracts repeated Lorentz indices in Mathematica-style ExpressionJSON;\n")
		fmt.Printf("reads from STDIN, writes the simplified expression to STDOUT.\n")
		fmt.Printf("Designed as a headless contraction kernel for FeynCalc/FeynGrav-style\n")
		fmt.Printf("symbolic computation pipelines.\n")
		fmt.Printf("\n")
		fmt.Printf("Source:  https://github.com/izokina/contractor\n")
		fmt.Printf("License: GPL-3.0\n")
		fmt.Printf("\n")
		fmt.Printf("Usage:\n")
		fmt.Printf("  contractor <flags>\n")
		fmt.Printf("\n")
		fmt.Printf("Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *version {
		fmt.Println("contractor", Version)
		return
	}

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal(err)
		}
		defer pprof.StopCPUProfile()
	}

	if *blockProfile != "" {
		runtime.SetBlockProfileRate(1)
		defer func() {
			f, err := os.Create(*blockProfile)
			if err != nil {
				log.Fatal(err)
			}
			defer f.Close()
			pprof.Lookup("block").WriteTo(f, 0)
		}()
	}
	if *mutexProfile != "" {
		runtime.SetMutexProfileFraction(1)
		defer func() {
			f, err := os.Create(*mutexProfile)
			if err != nil {
				log.Fatal(err)
			}
			defer f.Close()
			pprof.Lookup("mutex").WriteTo(f, 0)
		}()
	}

	runPipeline(os.Stdin, os.Stdout, *threads)

	if *memProfile != "" {
		f, err := os.Create(*memProfile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		runtime.GC()
		if err := pprof.WriteHeapProfile(f); err != nil {
			log.Fatal(err)
		}
	}
}
