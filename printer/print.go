package printer

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"strings"

	"github.com/algorand/msgp/gen"
	"github.com/algorand/msgp/parse"
	"github.com/daixiang0/gci/pkg/gci"
	"github.com/daixiang0/gci/pkg/gci/sections"
	"github.com/ttacon/chalk"
	"golang.org/x/tools/imports"
)

func infof(s string, v ...interface{}) {
	fmt.Printf(chalk.Magenta.Color(s), v...)
}

// PrintFile prints the methods for the provided list
// of elements to the given file name and canonical
// package path.
func PrintFile(file string, f *parse.FileSet, mode gen.Method, skipFormat bool) error {
	out, tests, err := generate(f, mode)
	if err != nil {
		return err
	}

	// we'll run goimports on the main file
	// in another goroutine, and run it here
	// for the test file. empirically, this
	// takes about the same amount of time as
	// doing them in serial when GOMAXPROCS=1,
	// and faster otherwise.
	res := goformat(file, out.Bytes(), skipFormat)
	if tests != nil {
		testfile := strings.TrimSuffix(file, ".go") + "_test.go"
		err = format(testfile, tests.Bytes(), skipFormat)
		if err != nil {
			return err
		}
		infof(">>> Wrote and formatted \"%s\"\n", testfile)
	}
	err = <-res
	if err != nil {
		return err
	}
	return nil
}

func format(file string, data []byte, skipFormat bool) error {
	if skipFormat {
		return ioutil.WriteFile(file, data, 0600)
	}
	// first run through goimports (which cleans up unused deps & does gofmt)
	out, err := imports.Process(file, data, nil)
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(file, out, 0600); err != nil {
		return err
	}
	// then run through gci to arrange import order
	if err := gci.WriteFormattedFiles([]string{file}, gci.GciConfiguration{
		Sections: gci.SectionList{
			sections.StandardPackage{},
			sections.DefaultSection{},
			sections.Prefix{ImportPrefix: "github.com/algorand"},
			sections.Prefix{ImportPrefix: "github.com/algorand/go-algorand"},
		},
		SectionSeparators: gci.SectionList{sections.NewLine{}},
	}); err != nil {
		return err
	}
	return nil
}

func goformat(file string, data []byte, skipFormat bool) <-chan error {
	out := make(chan error, 1)
	go func(file string, data []byte, end chan error) {
		end <- format(file, data, skipFormat)
		infof(">>> Wrote and formatted \"%s\"\n", file)
	}(file, data, out)
	return out
}

func dedupImports(imp []string) []string {
	m := make(map[string]struct{})
	for i := range imp {
		m[imp[i]] = struct{}{}
	}
	r := []string{}
	for k := range m {
		r = append(r, k)
	}
	return r
}

func generate(f *parse.FileSet, mode gen.Method) (*bytes.Buffer, *bytes.Buffer, error) {
	outbuf := bytes.NewBuffer(make([]byte, 0, 4096))
	writePkgHeader(outbuf, f.Package)

	myImports := []string{"github.com/algorand/msgp/msgp"}
	for _, imp := range f.Imports {
		if imp.Name != nil {
			// have an alias, include it.
			myImports = append(myImports, imp.Name.Name+` `+imp.Path.Value)
		} else {
			myImports = append(myImports, imp.Path.Value)
		}
	}
	dedup := dedupImports(myImports)
	writeImportHeader(outbuf, dedup...)

	var testbuf *bytes.Buffer
	var testwr io.Writer
	if mode&gen.Test == gen.Test {
		testbuf = bytes.NewBuffer(make([]byte, 0, 4096))
		writeBuildHeader(testbuf, []string{"!skip_msgp_testing"})
		writePkgHeader(testbuf, f.Package)
		writeImportHeader(
			testbuf,
			"github.com/algorand/msgp/msgp",
			"github.com/algorand/go-algorand/protocol",
			"github.com/algorand/go-algorand/test/partitiontest",
			"testing")
		testwr = testbuf
	}
	funcbuf := bytes.NewBuffer(make([]byte, 0, 4096))
	var topics gen.Topics

	err := f.PrintTo(gen.NewPrinter(mode, &topics, funcbuf, testwr))
	if err == nil {
		outbuf.Write(topics.Bytes())
		outbuf.Write(funcbuf.Bytes())
	}
	return outbuf, testbuf, err
}

func writePkgHeader(b *bytes.Buffer, name string) {
	b.WriteString("package ")
	b.WriteString(name)
	b.WriteByte('\n')
	// write generated code marker
	// https://github.com/tinylib/msgp/issues/229
	// https://golang.org/s/generatedcode
	b.WriteString("// Code generated by github.com/algorand/msgp DO NOT EDIT.\n\n")
}

func writeImportHeader(b *bytes.Buffer, imports ...string) {
	b.WriteString("import (\n")
	for _, im := range imports {
		if im[len(im)-1] == '"' {
			// support aliased imports
			fmt.Fprintf(b, "\t%s\n", im)
		} else {
			fmt.Fprintf(b, "\t%q\n", im)
		}
	}
	b.WriteString(")\n\n")
}

func writeBuildHeader(b *bytes.Buffer, buildHeaders []string) {
	headers := fmt.Sprintf("//go:build %s\n// +build %s\n\n", strings.Join(buildHeaders, " "), strings.Join(buildHeaders, " "))
	b.WriteString(headers)
}
