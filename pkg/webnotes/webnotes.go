package webnotes

import (
	"bufio"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	md "github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/ast"
	mdhtml "github.com/gomarkdown/markdown/html"
	mdparser "github.com/gomarkdown/markdown/parser"
)

const (
	IndexPath string = "wn_index"
	fileStart int    = 0
	inHeader  int    = 1
	inBody    int    = 2
)

// The order to put a section's fields in when writing a webnote file.
var orderedFieldNames []string = []string{"title", "description", "author", "date", "tags", "status", "error"}

// These fields can have only one value (they are not lists).
var singletonFieldNames []string = []string{"author", "date", "description", "error", "status", "title"}

// Struct for a section's header fields.
type Field struct {
	Name   string
	Values []string
}

// NewField returns an initialized Field.
func NewField(name string) *Field {
	return &Field{name, make([]string, 0)}
}

// Add adds the values in f2 to f.
// Values are not overwritten in f.
func (f *Field) Add(f2 *Field) {
	if slices.Contains(singletonFieldNames, f.Name) {
		return
	}
	for _, v := range f2.Values {
		if !slices.Contains(f.Values, v) {
			f.Values = append(f.Values, v)
		}
	}
}

// Struct for a section of a webnote file.
// One of Note or URL should be set.
type Section struct {
	Note   string
	URL    string
	Fields []*Field
	Body   []string
}

// NewSection returns an initialized Section.
// Only one of note or url should be passed to this function.
// An error is returned if both or neither of url are provided.
func NewSection(note, url string) (*Section, error) {
	if note == "" && url == "" {
		return nil, errors.New("note or url must be given")
	} else if note != "" && url != "" {
		return nil, errors.New("only a note or a url can be given")
	}
	return &Section{note, url, make([]*Field, 0), make([]string, 0)}, nil
}

// CompareSections compares two sections for equality and ordering.
// Only compares the Notes and URLs of the two sections.
// A section with Note set "comes before" a section with a URL set.
// Returns -1 if a is less than b.
// Returns 0 if a equals b.
// Returns 1 if a is greater than b.
func CompareSections(a, b *Section) int {
	if a.Note != "" && b.Note != "" {
		if a.Note < b.Note {
			return -1
		} else {
			return 1
		}
	}
	if a.URL != "" && b.URL != "" {
		if a.URL < b.URL {
			return -1
		} else {
			return 1
		}
	}
	if a.Note != "" {
		return -1
	}
	if b.Note != "" {
		return 1
	}
	if a.URL != "" {
		return -1
	}
	if b.URL != "" {
		return 1
	}
	return 0
}

// ContentTitle returns a title from the goquery document.
// Extra whitespace is removed from the title.
func ContentTitle(doc *goquery.Document) string {
	title := RemoveExtraWhitespace(doc.Find("title").Text())
	return title
}

// ContentImages returns the images found in the goquery document.
// Images are returned in Markdown format.
func ContentImages(doc *goquery.Document) []string {
	lines := []string{}
	f := func(i int, s *goquery.Selection) bool {
		src, _ := s.Attr("src")
		return strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://")
	}
	doc.Find("body img").FilterFunction(f).Each(
		func(_ int, tag *goquery.Selection) {
			src, _ := tag.Attr("src")
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, fmt.Sprintf("![alt text](%s \"title\")", src))
		})
	return lines
}

// ContentLinks returns the links found in the goquery document.
// Links are returned in Markdown format.
func ContentLinks(doc *goquery.Document) []string {
	lines := []string{}
	f := func(i int, s *goquery.Selection) bool {
		link, _ := s.Attr("href")
		return strings.HasPrefix(link, "https://") || strings.HasPrefix(link, "http://")
	}
	doc.Find("body a").FilterFunction(f).Each(
		func(_ int, tag *goquery.Selection) {
			link, _ := tag.Attr("href")
			linkText := tag.Text()
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, fmt.Sprintf("[%s](%s)", linkText, link))
		})
	return lines
}

// ContentP returns the text content of the goquery document.
// It searches for the content between <p></p> html tags.
// It removes extra whitespace.
func ContentP(doc *goquery.Document) []string {
	content := []string{}
	doc.Find("p").Each(func(_ int, s *goquery.Selection) {
		text := RemoveExtraWhitespace(s.Text())
		if len(text) > 0 {
			if len(content) > 0 {
				content = append(content, "")
			}
			content = append(content, text)
		}
	})
	return content
}

// ContentText returns the text content of the goquery document.
func ContentText(doc *goquery.Document) []string {
	lines := []string{}
	body := doc.Find("body")
	scanner := bufio.NewScanner(strings.NewReader(body.Text()))
	for scanner.Scan() {
		text := scanner.Text()
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, text)
	}
	return lines
}

// Add adds the content of one section to another.
// Values in s are not overwritten by values in s2.
func (s *Section) Add(s2 *Section) {
	for _, inField := range s2.Fields {
		outField, ok := s.Field(inField.Name)
		if ok {
			outField.Add(inField)
		} else {
			s.AddField(inField.Name, inField.Values)
		}
	}
	if len(s2.Body) > 0 {
		if len(s.Body) > 0 {
			s.Body = append(s.Body, "")
		}
		s.Body = append(s.Body, s2.Body...)
	}
}

// AddTag adds the provided tag to the section.
// Duplicate tags are removed.
func (s *Section) AddTag(tag string) {
	field, ok := s.Field("tags")
	if !ok {
		s.AddField("tags", []string{tag})
		return
	}
	field.Values = append(field.Values, tag)
	// this gets rid of duplicates
	slices.Sort[[]string](field.Values)
	field.Values = slices.Compact[[]string, string](field.Values)
}

// AddTags adds the provided tags to the section.
// Duplicate tags are removed.
func (s *Section) AddTags(tags []string) {
	for _, tag := range tags {
		s.AddTag(tag)
	}
}

// AddField adds the to the section.
// TODO: check if field already exists and overwrite it?
func (s *Section) AddField(name string, values []string) {
	s.Fields = append(s.Fields, &Field{name, values})
}

// AppendBody adds the line to the end of the section's body.
func (s *Section) AppendBody(line string) {
	s.Body = append(s.Body, line)
}

// DeleteAll deletes all the fields and the body from a section.
func (s *Section) DeleteAll() {
	s.DeleteAllFields()
	s.DeleteBody()
}

// DeleteAllFields deletes all the fields from a section.
func (s *Section) DeleteAllFields() {
	s.Fields = make([]*Field, 0)
}

// DeleteBody deletes the body from a section.
func (s *Section) DeleteBody() {
	s.Body = make([]string, 0)
}

// DeleteField deletes the field from a section.
// The field to delete is specified by name.
func (s *Section) DeleteField(name string) {
	s.Fields = slices.DeleteFunc(s.Fields, func(f *Field) bool { return f.Name == name })
}

// DeleteFields deletes the provided fields from the section.
// The fields to delete are specified by name.
func (s *Section) DeleteFields(names ...string) {
	for _, name := range names {
		s.DeleteField(name)
	}
}

// DeleteTag deletes the provided tag from the section.
// It deletes the tags field if there are no tags left.
func (s *Section) DeleteTag(tag string) {
	field, ok := s.Field("tags")
	if !ok {
		return
	}
	values := []string{}
	for _, value := range field.Values {
		if value != tag {
			values = append(values, value)
		}
	}
	if len(values) > 0 {
		field.Values = values
	} else {
		s.DeleteField("tags")
	}
}

// DeleteTags deletes the provided tags from the section.
// It deletes the tags field if no tags are left.
func (s *Section) DeleteTags(tags []string) {
	for _, tag := range tags {
		s.DeleteTag(tag)
	}
}

// EqualsHost checks the host of the URL of the section for equality.
// Returns true if the two are equal and false otherwise.
func (s *Section) EqualsHost(host string) bool {
	if s.URL == "" {
		return false
	}
	url_, err := url.Parse(s.URL)
	if host == "" {
		return true
	}
	if err != nil {
		// TODO: ok to swallow this error?
		return false
	}
	return url_.Host == host
}

// Field returns the field from the Section with the provided name.
// Returns *Field, true if the Section has the Section.
// Returns nil, false if the Section does not have the field.
func (s *Section) Field(name string) (*Field, bool) {
	for _, field := range s.Fields {
		if name == field.Name {
			return field, true
		}
	}
	return nil, false
}

// FieldEqualsValue returns true if the field equals the provided value.
// It accepts the name of the field and the value to test.
// Returns true if the field exists and has the provided value.
func (s *Section) FieldEqualsValue(name string, value string) bool {
	field, ok := s.Field(name)
	if !ok {
		return false
	}
	if len(field.Values) != 1 {
		return false
	}
	return field.Values[0] == value
}

// FieldHasValue checks if a section's field has one or more values.
// The field is specified by name.
// The values to be checked are provided as a slice of strings.
// Returns true if the field exists and has at least one of the values to check.
func (s *Section) FieldHasValue(name string, values []string) bool {
	if len(values) == 0 {
		return true
	}
	fieldValues, ok := s.FieldValues(name)
	if !ok {
		return false
	}
	count := 0
	for _, value := range values {
		if slices.Contains(fieldValues, value) {
			count++
		}
	}
	return count > 0
}

// FieldHasValues checks if a section's field has all of the provided values.
// The field is specified by name.
// The values to be checked are provided as a slice of strings.
// Returns true if the field exits and all values to check are found.
func (s *Section) FieldHasValues(name string, values []string) bool {
	if len(values) == 0 {
		return true
	}
	fieldValues, ok := s.FieldValues(name)
	if !ok {
		return false
	}
	count := 0
	for _, value := range values {
		if slices.Contains(fieldValues, value) {
			count++
		}
	}
	return count == len(values)
}

// FieldValue returns the value of a section's field.
// It returns (value, true) if the field is found and it has one value.
// It returns ("", false) if the field is not found or the field is set to a list of values.
func (s *Section) FieldValue(name string) (string, bool) {
	for _, field := range s.Fields {
		if name == field.Name {
			if len(field.Values) == 1 {
				return field.Values[0], true
			}
		}
	}
	return "", false
}

// FieldValues returns the values of a section's field.
// It returns (values, true) if the field is found.
// It returns (nil, false) if the field is not found.
func (s *Section) FieldValues(name string) ([]string, bool) {
	for _, field := range s.Fields {
		if name == field.Name {
			return field.Values, true
		}
	}
	return nil, false
}

// FillBody sets the body of the section to the lines provided if the section does not already have a body.
func (s *Section) FillBody(lines []string) {
	if len(s.Body) == 0 {
		s.Body = lines
	}
}

// FillDate sets the date field of the section to the current date if the date is not areadly set.
func (s *Section) FillDate() {
	_, ok := s.Field("date")
	if !ok {
		s.SetDate()
	}
}

// FillFieldValue sets the value of the field if it does not already have a value.
// Field is specified by name.
// Value is provided as a string.
func (s *Section) FillFieldValue(name string, value string) {
	_, ok := s.Field(name)
	if !ok {
		s.SetFieldValue(name, value)
	}
}

// Get gets the html document for the URL of the section.
// Returns (*goquery.Document, nil) on success.
// Returns (nil, error) if there is an error.
// Anything besides a 200 status code for the request is considered an error.
// Sets the section's status if for status codes other than 200.
// Sets the section's error if there is some error (besides an unexpected status).
func (s *Section) Get() (*goquery.Document, error) {
	if s.URL == "" {
		return nil, errors.New("Section does not have a url")
	}
	resp, err := http.Get(s.URL)
	if err != nil {
		s.SetError(err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		s.SetStatus(resp.Status)
		return nil, errors.New("Failed to get document")
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		s.SetError(err)
		return nil, err
	}
	return doc, nil
}

// HasField returns true if the section has the field.
// The field is specified by name.
func (s *Section) HasField(name string) bool {
	for _, f := range s.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

// Head does a head on the url of the section.
// It sets the error field for the section if there is an error.
// It sets the status for the section on status codes besides a 200.
// On a successful head, it deltes the error and status fields of the section.
func (s *Section) Head() {
	if s.URL == "" {
		s.SetError(errors.New("Section does not have a url"))
		return
	}
	resp, err := http.Head(s.URL)
	if err != nil {
		s.SetError(err)
	} else if resp.StatusCode == 200 {
		s.DeleteFields("error", "status")
	} else {
		s.SetStatus(resp.Status)
	}
}

// Host returns the host of the section's URL.
// Returns (host, nil) on success.
// Returns ("", error) on error.
// If the section does not have a URL, this results in an error.
func (s *Section) Host() (string, error) {
	if s.URL == "" {
		return "", errors.New("Section does not have a url")
	}
	url_, err := url.Parse(s.URL)
	if err != nil {
		return "", err
	}
	return url_.Host, nil
}

// ID returns the section's identification string.
// A section's ID is its Note or URL.
// The URL includes the http:// or https:// at the start.
// The Note does not include the note:// at the start.
// Returns (id, nil) on success.
// Returns ("", error) on error.
func (s *Section) ID() (string, error) {
	if s.Note == "" && s.URL == "" {
		return "", errors.New("Section has no note and url")
	} else if s.Note != "" && s.URL != "" {
		return "", errors.New("Section has both note and url")
	} else if s.Note != "" {
		return s.Note, nil
	} else {
		return s.URL, nil
	}
}

// Matches returns true if the two sections match.
// Matching means their Notes or URLs match.
func (s *Section) Matches(s2 *Section) bool {
	if s.Note != "" {
		return s.Note == s2.Note
	} else if s.URL != "" {
		return s.URL == s2.URL
	} else {
		return s2.Note == "" && s2.URL == ""
	}
}

// SetBody sets the section's body.
func (s *Section) SetBody(lines []string) {
	s.Body = lines
}

// SetDate sets the section's date to the current date.
func (s *Section) SetDate() {
	date := time.Now().Format(time.DateOnly)
	s.SetFieldValue("date", date)
}

// SetError sets the section's error field.
func (s *Section) SetError(err error) {
	s.SetFieldValue("error", err.Error())
	s.DeleteField("status")
}

// SetField sets the field for the section to the slice of provided strings.
func (s *Section) SetField(name string, values []string) {
	field, ok := s.Field(name)
	if ok {
		field.Values = values
	} else {
		s.AddField(name, values)
	}
}

// SetFieldValue sets the field for the section to the given value.
func (s *Section) SetFieldValue(name string, value string) {
	s.SetField(name, []string{value})
}

// SetFieldValues sets the field for the section to the slice of provided strings.
func (s *Section) SetFieldValues(name string, values []string) {
	s.SetField(name, values)
}

// SetStatus sets the status field for the section to the provided status.
func (s *Section) SetStatus(status string) {
	s.DeleteField("error")
	s.SetFieldValue("status", status)
}

// SetTags sets the tags field for the section to the provided slice of values.
func (s *Section) SetTags(tags []string) {
	if len(tags) == 0 {
		s.DeleteField("tags")
	} else {
		s.SetFieldValues("tags", tags)
	}
}

// String returns a string value of the section.
// The string is suitable for writing to a webnote file.
func (s *Section) String() string {
	lines := make([]string, 0)
	if s.Note != "" {
		lines = append(lines, fmt.Sprintf("# note://%s", s.Note))
	} else if s.URL != "" {
		lines = append(lines, fmt.Sprintf("# %s", s.URL))
	} else {
		// this should never happen if the section is properly formed
		lines = append(lines, "# https://example.com")
	}
	for _, name := range orderedFieldNames {
		field, ok := s.Field(name)
		if ok {
			if name == "tags" {
				slices.Sort[[]string](field.Values)
			}
			if len(field.Values) > 0 {
				lines = append(lines, fmt.Sprintf("%s: %s", field.Name, strings.Join(field.Values, ",")))
			}
		}
	}
	for _, field := range s.Fields {
		if slices.Contains(orderedFieldNames, field.Name) {
			continue
		}
		if len(field.Values) > 0 {
			lines = append(lines, fmt.Sprintf("%s: %s", field.Name, strings.Join(field.Values, ",")))
		}
	}
	inBody := false
	for _, line := range s.Body {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		if line == "" {
			if inBody {
				if lines[len(lines)-1] == "" {
					continue
				}
			} else {
				continue
			}
		}
		if !inBody {
			lines = append(lines, "")
			inBody = true
		}
		lines = append(lines, line)
	}
	if lines[len(lines)-1] == "" {
		lines = lines[0 : len(lines)-1]
	}
	return strings.Join(lines, "\n") + "\n"
}

// Struct for a webnote file.
// FilePath is the path on disk for the file.
// Sections is a slice of the sections of th WebNote in order.
type WebNote struct {
	FilePath string
	Sections []*Section
}

// NewWebNote returns an initialized WebNote.
func NewWebNote(filePath string) *WebNote {
	return &WebNote{filePath, make([]*Section, 0)}
}

// AddSection adds a seciton to the WebNote.
// Section is added to the end of the list of sections.
func (wn *WebNote) AddSection(section *Section) {
	wn.Sections = append(wn.Sections, section)
}

// formatLastSection formats the last section of the WebNote.
// Right now this formatting is only removing a blank line at the end of the Body of the section.
func (wn *WebNote) formatLastSection() {
	if len(wn.Sections) > 0 {
		section := wn.Sections[len(wn.Sections)-1]
		if len(section.Body) > 0 {
			if section.Body[len(section.Body)-1] == "" {
				section.Body = section.Body[0 : len(section.Body)-1]
			}
		}
	}
}

// formatNoteString formats the note string.
// This removes extra spaces and turns the remaining spaces into underscores.
func formatNoteString(noteString string) (string, error) {
	ns := strings.TrimSpace(noteString)
	parts := strings.Fields(ns)
	return strings.Join(parts, "_"), nil
}

// LoadWebNote file loads a WebNote from the filePath provided.
// Returns (*WebNote, nil) on success.
// Returns (nil, error) on failure.
func LoadWebNote(filePath string) (*WebNote, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	webNote := NewWebNote(filePath)
	parseState := fileStart
	var section *Section
	lineNumber := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		lineNumber += 1
		if strings.HasPrefix(line, "# note://") {
			webNote.formatLastSection()
			noteString, err := formatNoteString(line[len("# note://"):])
			if err != nil {
				return nil, err
			}
			section, err = NewSection(noteString, "")
			if err != nil {
				return nil, err
			}
			webNote.AddSection(section)
			parseState = inHeader
		} else if strings.HasPrefix(line, "# http://") || strings.HasPrefix(line, "# https://") {
			webNote.formatLastSection()
			section, err = NewSection("", line[len("# "):])
			if err != nil {
				return nil, err
			}
			webNote.AddSection(section)
			parseState = inHeader
		} else if parseState == inHeader {
			if line == "" {
				parseState = inBody
			} else {
				parts := strings.SplitN(line, ": ", 2)
				if len(parts) != 2 {
					return nil, errorWithLineNumber(errors.New("Invalid header line"), lineNumber)
				}
				var values []string
				if slices.Contains(singletonFieldNames, parts[0]) {
					values = []string{parts[1]}
				} else {
					values = strings.Split(parts[1], ",")
				}
				if len(values) == 0 {
					return nil, errorWithLineNumber(errors.New("Invalid header line"), lineNumber)
				}
				section.AddField(parts[0], values)
			}
		} else if parseState == inBody {
			section.AppendBody(line)
		} else if parseState == fileStart {
			return nil, errorWithLineNumber(errors.New("Unexpected start to web note file"), lineNumber)
		} else {
			return nil, errorWithLineNumber(errors.New("Unexpected parsing error"), lineNumber)
		}
	}
	return webNote, nil
}

// errorWithLineNumber makes an error with a line number.
// This is used when parsing a WebNote file.
// The line number helps users find were the problem is in their file.
func errorWithLineNumber(err error, lineNumber int) error {
	return errors.New(err.Error() + " on line " + strconv.Itoa(lineNumber))
}

// GetWebNoteFiles gets the WebNote file paths from the provided directory path.
// This function walks the directory tree looking for WebNote files.
// Returns ([]file_paths, nil) on success.
// Returns (nil, error) on failure.
func GetWebNoteFiles(directoryPath string) ([]string, error) {
	files := make([]string, 0)
	walk := func(path string, f os.FileInfo, err error) error {
		if f.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".wn") {
			return nil
		}
		if strings.Contains(path, IndexPath) {
			return nil
		}
		files = append(files, path)
		return nil
	}
	err := filepath.Walk(directoryPath, walk)
	if err != nil {
		return nil, err
	}
	return files, nil
}

// SaveWebNote file writes the WebNote to disk.
// Returns nil on success and error on failure.
func SaveWebNote(wn *WebNote) error {
	file, err := os.Create(wn.FilePath)
	if err != nil {
		return err
	}
	defer file.Close()
	wroteSection := false
	for _, section := range wn.Sections {
		if section == nil {
			continue
		}
		if wroteSection {
			file.WriteString("\n")
		}
		file.WriteString(section.String())
		wroteSection = true
	}
	return nil
}

// Structure used when building a WebNote index.
type NameWebNote struct {
	Name     string
	WebNote_ *WebNote
}

// Structure used when building a WebNote index.
type IndexEntry struct {
	Name string
	MD5  string
}

// Structure used when building a WebNote index.
type FilePathNote struct {
	FilePath string
	Note     string
}

// BuildIndex builds a WebNote index.
// IndexPath is removed if it exists.
// IndexPath is created and the index is written there.
// The current working directory is where WebNote files are searched for.
func BuildIndex() error {
	if stat, err := os.Stat(IndexPath); err == nil {
		if stat.IsDir() {
			if err := os.RemoveAll(IndexPath); err != nil {
				return err
			}
		} else {
			return errors.New(fmt.Sprintf("Error: %s file exists", IndexPath))
		}
	}
	indexDirs := []string{"authors", "hosts", "notes", "tags"}
	for _, dir := range indexDirs {
		indexDir := filepath.Join(IndexPath, dir)
		if err := os.MkdirAll(indexDir, os.ModePerm); err != nil {
			return err
		}
	}
	files, err := GetWebNoteFiles(".")
	if err != nil {
		return err
	}
	authors := make(map[string]*NameWebNote)
	hosts := make(map[string]*NameWebNote)
	notes := make(map[string]*FilePathNote)
	tags := make(map[string]*NameWebNote)
	for _, filePath := range files {
		wn, err := LoadWebNote(filePath)
		if err != nil {
			return err
		}
		for _, sct := range wn.Sections {
			if sct.Note != "" {
				key := fmt.Sprintf("%s#%s", filePath, sct.Note)
				_, ok := notes[key]
				if ok {
					return errors.New(fmt.Sprintf("Found duplicate note section: %s", key))
				}
				notes[key] = &FilePathNote{filePath, sct.Note}
			} else if sct.URL != "" {
				host, err := sct.Host()
				if err == nil {
					md5_ := fmt.Sprintf("%x", md5.Sum([]byte(host)))
					ie, ok := hosts[md5_]
					if !ok {
						filePath := filepath.Join(IndexPath, "hosts", fmt.Sprintf("%s.wn", md5_))
						ie = &NameWebNote{host, NewWebNote(filePath)}
						hosts[md5_] = ie
					}
					ie.WebNote_.AddSection(sct)
				}
			} else {
				return errors.New(fmt.Sprintf("Found section with neither note or url: %s", filePath))
			}
			value, ok := sct.FieldValue("author")
			if ok {
				md5_ := fmt.Sprintf("%x", md5.Sum([]byte(value)))
				ie, ok := authors[md5_]
				if !ok {
					filePath := filepath.Join(IndexPath, "authors", fmt.Sprintf("%s.wn", md5_))
					ie = &NameWebNote{value, NewWebNote(filePath)}
					authors[md5_] = ie
				}
				ie.WebNote_.AddSection(sct)
			}
			values, ok := sct.FieldValues("tags")
			if ok {
				for _, tag := range values {
					md5_ := fmt.Sprintf("%x", md5.Sum([]byte(tag)))
					ie, ok := tags[md5_]
					if !ok {
						filePath := filepath.Join(IndexPath, "tags", fmt.Sprintf("%s.wn", md5_))
						ie = &NameWebNote{tag, NewWebNote(filePath)}
						tags[md5_] = ie
					}
					ie.WebNote_.AddSection(sct)
				}
			}
		}
	}
	var filePath string
	filePath = filepath.Join(IndexPath, "authors", "index")
	if err := SaveIndexFile(filePath, authors); err != nil {
		return err
	}
	filePath = filepath.Join(IndexPath, "hosts", "index")
	if err := SaveIndexFile(filePath, hosts); err != nil {
		return err
	}
	filePath = filepath.Join(IndexPath, "notes", "index")
	if err := SaveNoteIndexFile(filePath, notes); err != nil {
		return err
	}
	filePath = filepath.Join(IndexPath, "tags", "index")
	if err := SaveIndexFile(filePath, tags); err != nil {
		return err
	}
	return nil
}

// LoadIndexFile loads an index file.
// Returns ([]*IndexEntry, nil) on success.
// Returns (nil, error) on failure.
func LoadIndexFile(filePath string) ([]*IndexEntry, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	index := []*IndexEntry{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			return nil, errors.New("Invalid index line")
		}
		index = append(index, &IndexEntry{parts[1], parts[0]})
	}
	return index, nil
}

// LoadFile loads a file from the provided file path.
// returns ([]lines_in_file, nil) on success.
// returns (nil, error) on failure.
func LoadFile(filePath string) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	lines := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, nil
}

// NameFromIndex searches an index for a name of an entry matching the provided MD5.
func NameFromIndex(index []*IndexEntry, md5_ string) (string, error) {
	for _, ie := range index {
		if ie.MD5 == md5_ {
			return ie.Name, nil
		}
	}
	return "", errors.New("Unable to find index name")
}

// SaveIndexFile writes an index to a file.
// Returns nil on success.
// Returns error on failure.
func SaveIndexFile(filePath string, index map[string]*NameWebNote) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	type indexLine struct {
		md5_ string
		name string
	}
	indexLines := make([]*indexLine, 0, len(index))
	for md5_, ie := range index {
		if err := SaveWebNote(ie.WebNote_); err != nil {
			return err
		}
		indexLines = append(indexLines, &indexLine{md5_, ie.Name})
	}
	sort.Slice(indexLines, func(i, j int) bool { return indexLines[i].name < indexLines[j].name })
	for _, line := range indexLines {
		fmt.Fprintf(file, "%s: %s\n", line.md5_, line.name)
	}
	return nil
}

// SaveNoteIndexFile saves a note index file to disk.
// Returns nil on success.
// Returns error on failure.
func SaveNoteIndexFile(filePath string, index map[string]*FilePathNote) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	keys := make([]string, 0, len(index))
	for k := range index {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fpn := index[k]
		fmt.Fprintf(file, "%s#%s\n", fpn.FilePath, fpn.Note)
	}
	return nil
}

// FileExists checks to see if a file exists at the provided path.
// Returns (true, nil) if the file exists.
// Returns (false, nil) if the file does not exist.
// Returns (false, error) if there is a failure.
func FileExists(filePath string) (bool, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if fileInfo.IsDir() {
		return false, errors.New("File path is a directory")
	}
	return true, nil
}

// GetTags returns a list of tags from a tag string.
// A tag string is a comma separated list of tags.
// Returns ([]tags, nil) on success.
// Returns (nil, error) on failure.
// TODO: check for valid tag strings and return an error if an invalid tag is found.
func GetTags(tagsString string) ([]string, error) {
	if len(tagsString) == 0 {
		return make([]string, 0), nil
	}
	return strings.Split(tagsString, ","), nil
}

// MarkdownToHTML returns HTML from a string containing markdown.
func MarkdownToHTML(markdown string) string {
	extensions := mdparser.CommonExtensions | mdparser.AutoHeadingIDs | mdparser.NoEmptyLineBeforeBlock
	p := mdparser.NewWithExtensions(extensions)
	doc := p.Parse([]byte(markdown))

	isWebNoteLink := func(dest string) bool {
		if strings.HasPrefix(dest, "http://") || strings.HasPrefix(dest, "https://") {
			return false
		}
		if strings.HasSuffix(dest, ".wn") {
			return false
		}
		return strings.Contains(dest, ".wn#")
	}

	renderHookLink := func(w io.Writer, node ast.Node, entering bool) (ast.WalkStatus, bool) {
		link, ok := node.(*ast.Link)
		if !ok {
			return ast.GoToNext, false
		}
		if entering {
			dest := string(link.Destination)
			if !isWebNoteLink(dest) {
				return ast.GoToNext, false
			}
			io.WriteString(w, fmt.Sprintf("<a href=\"/file/%s\">", dest))
			return ast.GoToNext, true
		}
		return ast.GoToNext, false
	}

	htmlFlags := mdhtml.CommonFlags | mdhtml.HrefTargetBlank
	opts := mdhtml.RendererOptions{
		Flags:          htmlFlags,
		RenderNodeHook: renderHookLink,
	}
	renderer := mdhtml.NewRenderer(opts)

	return string(md.Render(doc, renderer))
}

// ExtraWhitespace removes extra whitespace from a string.
// This collapses all instances of consecutive whitespace to a single space.
// This also trims space for the start and end of the string.
func RemoveExtraWhitespace(text string) string {
	re := regexp.MustCompile(`\s+`)
	return strings.TrimSpace(re.ReplaceAllLiteralString(text, " "))
}
