package main

import (
	"encoding/json/jsontext"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"unicode/utf8"

	"github.com/izokina/contractor/pkg/pipeline/ident"
	"github.com/izokina/contractor/pkg/pipeline/merge"
	"github.com/izokina/contractor/pkg/pipeline/worker"
)

const Version = "0.3.0"

// asciiStderr escapes non-ASCII bytes: diagnostics quote input text, and the
// tool's own output must stay ASCII.
type asciiStderr struct{}

func (asciiStderr) Write(p []byte) (int, error) {
	out := make([]byte, 0, len(p))
	for rest := p; len(rest) > 0; {
		if rest[0] < utf8.RuneSelf {
			out = append(out, rest[0])
			rest = rest[1:]
			continue
		}
		_, n := utf8.DecodeRune(rest)
		q := strconv.QuoteToASCII(string(rest[:n]))
		out = append(out, q[1:len(q)-1]...)
		rest = rest[n:]
	}
	if _, err := os.Stderr.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

func fail(code int, err error) {
	fmt.Fprintln(asciiStderr{}, err)
	os.Exit(code)
}

func runPipeline(in io.Reader, out io.Writer, threads, mergeMemory int) error {
	t := ident.NewTable()
	merger := merge.NewMerger(t, mergeMemory)
	if err := worker.Run(in, t, merger, threads); err != nil {
		return errors.Join(err, merger.Close())
	}

	enc := jsontext.NewEncoder(out, jsontext.WithIndent("\t"))
	if err := merger.Flush(enc); err != nil {
		return fmt.Errorf("output incomplete: %w", err)
	}
	return nil
}

// createProfile runs before the pipeline, so that a bad path fails before any
// work.
func createProfile(path string) *os.File {
	if path == "" {
		return nil
	}
	f, err := os.Create(path)
	if err != nil {
		fail(1, err)
	}
	return f
}

func writeProfile(f *os.File, name string) error {
	if f == nil {
		return nil
	}
	return errors.Join(pprof.Lookup(name).WriteTo(f, 0), f.Close())
}

func main() {
	flag.CommandLine.SetOutput(asciiStderr{})

	threads := flag.Int("threads", runtime.NumCPU(), "Number of worker threads, defaults to CPU number")
	mergeMemory := flag.Int("merge-memory", 0, "Bytes of merged terms to hold in memory before moving them to a temporary file; 0 holds all")
	cpuProfile := flag.String("cpuprofile", "", "Write CPU profile to file")
	memProfile := flag.String("memprofile", "", "Write memory profile to file")
	blockProfile := flag.String("blockprofile", "", "Write block profile to file")
	mutexProfile := flag.String("mutexprofile", "", "Write mutex profile to file")
	version := flag.Bool("version", false, "Print version and exit")

	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "contractor %s - High-performance tensor index contractor.\n", Version)
		fmt.Fprintf(out, "\n")
		fmt.Fprintf(out, "Contracts repeated Lorentz indices in Mathematica-style ExpressionJSON;\n")
		fmt.Fprintf(out, "reads from STDIN, writes the simplified expression to STDOUT.\n")
		fmt.Fprintf(out, "Designed as a headless contraction kernel for FeynCalc/FeynGrav-style\n")
		fmt.Fprintf(out, "symbolic computation pipelines.\n")
		fmt.Fprintf(out, "\n")
		fmt.Fprintf(out, "Source:  https://github.com/izokina/contractor\n")
		fmt.Fprintf(out, "License: GPL-3.0\n")
		fmt.Fprintf(out, "\n")
		fmt.Fprintf(out, "Usage:\n")
		fmt.Fprintf(out, "  contractor <flags>\n")
		fmt.Fprintf(out, "\n")
		fmt.Fprintf(out, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *version {
		fmt.Println("contractor", Version)
		return
	}

	if flag.NArg() != 0 {
		fail(2, fmt.Errorf("unexpected argument %q: the input is read from stdin", flag.Arg(0)))
	}
	if *threads < 1 {
		fail(2, fmt.Errorf("invalid value %d for flag -threads: must be at least 1", *threads))
	}
	if *mergeMemory < 0 {
		fail(2, fmt.Errorf("invalid value %d for flag -merge-memory: must not be negative", *mergeMemory))
	}

	cpu := createProfile(*cpuProfile)
	mem := createProfile(*memProfile)
	block := createProfile(*blockProfile)
	mutex := createProfile(*mutexProfile)
	if cpu != nil {
		if err := pprof.StartCPUProfile(cpu); err != nil {
			fail(1, err)
		}
	}
	if block != nil {
		runtime.SetBlockProfileRate(1)
	}
	if mutex != nil {
		runtime.SetMutexProfileFraction(1)
	}

	err := runPipeline(os.Stdin, os.Stdout, *threads, *mergeMemory)

	if cpu != nil {
		pprof.StopCPUProfile()
		err = errors.Join(err, cpu.Close())
	}
	if mem != nil {
		runtime.GC()
	}
	err = errors.Join(err, writeProfile(mem, "heap"), writeProfile(block, "block"), writeProfile(mutex, "mutex"))
	if err != nil {
		fail(1, err)
	}
}
