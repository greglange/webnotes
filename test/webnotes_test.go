package test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/greglange/webnotes/pkg/webnotes"
)

type exitCodeError struct {
	expected, actual int
}

func (e exitCodeError) Error() string {
	return fmt.Sprintf("Expected error code '%d' but got '%d'", e.expected, e.actual)
}

func removeFile(filePath string) error {
	info, err := os.Stat(filePath)
	if err == nil {
		if info.IsDir() {
			return errors.New("Path is directory")
		}
		return os.Remove(filePath)
	} else {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
}

func runWebnotes(exitCode int, arg []string) (string, error) {
	cmd := exec.Command("webnotes", arg...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	ec := cmd.ProcessState.ExitCode()
	if ec != exitCode {
		return out.String(), exitCodeError{exitCode, ec}
	}
	return out.String(), err
}

func TestNoCammandLineFlags(t *testing.T) {
	output, err := runWebnotes(1, []string{})
	if err == nil {
		t.Fatal("Expected failure")
	}
	_, ok := err.(*exitCodeError)
	if ok {
		t.Fatal(err)
	}
	if output != "You must choose a main option\n" {
		t.Fatalf("Unexpected output: %s", output)
	}
}

func TestInvalidFlag(t *testing.T) {
	output, err := runWebnotes(2, []string{"--invalid_flag"})
	if err == nil {
		t.Fatal("Expected failure")
	}
	_, ok := err.(*exitCodeError)
	if ok {
		t.Fatal(err)
	}
	// TODO: test entire message by moving usage string to package?
	if !strings.HasPrefix(output, "Usage of webnotes:") {
		t.Fatalf("Expected usage message but got: %s", output)
	}
}

func TestHelpFlag(t *testing.T) {
	output, err := runWebnotes(0, []string{"--help"})
	if err != nil {
		t.Fatalf("Unexpected failure: %s", err)
	}
	// TODO: test entire message by moving usage string to package?
	if !strings.HasPrefix(output, "Usage of webnotes:") {
		t.Fatalf("Expected usage message but got: %s", output)
	}
}

func TestAdd(t *testing.T) {
	type test struct {
		flags    []string
		filePath string
		note     string
		url      string
	}
	tests := []test{
		{
			[]string{"--add", "--out_file", "Test.wn", "--vnote", "some_note"},
			"Test.wn",
			"some_note",
			"",
		},
		{
			[]string{"--add", "--out_file", "Test.wn", "--vurl", "https://example.com"},
			"Test.wn",
			"",
			"https://example.com",
		},
	}
	flagsFields := func(i int) ([]string, []*webnotes.Field) {
		flags := []string{}
		fields := []*webnotes.Field{}
		// the order here is important - see orderedFieldNames
		if i&1 == 1 {
			flags = append(flags, "-vtitle", "Some title")
			fields = append(fields, &webnotes.Field{"title", []string{"Some title"}})
		}
		if i&2 == 2 {
			flags = append(flags, "-vdescription", "Some description")
			fields = append(fields, &webnotes.Field{"description", []string{"Some description"}})
		}
		if i&4 == 4 {
			flags = append(flags, "-vauthor", "Some Author")
			fields = append(fields, &webnotes.Field{"author", []string{"Some Author"}})
		}
		if i&8 == 8 {
			flags = append(flags, "-vdate", "2024-01-01")
			fields = append(fields, &webnotes.Field{"date", []string{"2024-01-01"}})
		}
		if i&16 == 16 {
			flags = append(flags, "-vtags", "one,two,three")
			fields = append(fields, &webnotes.Field{"tags", []string{"one", "three", "two"}})
		}
		return flags, fields
	}
	for _, tc := range tests {
		for _, body := range []string{"", "Some body"} {
			for i := 0; i < 33; i++ {
				defer removeFile(tc.filePath)
				flags, fields := flagsFields(i)
				flags = append(tc.flags, flags...)
				if len(body) > 0 {
					flags = append(flags, "--vbody", body)
				}
				output, err := runWebnotes(0, flags)
				if err != nil {
					t.Fatalf("%s: run webnotes failure: %s", flags, err)
				}
				if output != "" {
					t.Fatalf("%s: unexpected output: %s", flags, output)
				}
				wn, err := webnotes.LoadWebNote(tc.filePath)
				if err != nil {
					t.Fatalf("%s: load web note failure: %s", flags, err)
				}
				if tc.filePath != wn.FilePath {
					t.Fatalf("%s: unexpected file path: %s", flags, wn.FilePath)
				}
				if 1 != len(wn.Sections) {
					t.Fatalf("%s: unexpected number of sections: %d", flags, len(wn.Sections))
				}
				expSct, err := webnotes.NewSection(tc.note, tc.url)
				if err != nil {
					t.Fatalf("%s: unexpected new section failure: %s", flags, err)
				}
				expSct.Fields = fields
				if len(body) > 0 {
					expSct.Body = []string{body}
				}
				if !reflect.DeepEqual(expSct, wn.Sections[0]) {
					t.Fatalf("%s: unexpected section content: %s", flags, wn.Sections[0])
				}
				removeFile(tc.filePath)
			}
		}
	}
}
