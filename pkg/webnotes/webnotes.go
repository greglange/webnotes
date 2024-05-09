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

var orderedFieldNames []string = []string{"title", "description", "author", "date", "tags", "status", "error"}
var singletonFieldNames []string = []string{"author", "date", "description", "error", "status", "title"}

type Field struct {
	Name   string
	Values []string
}

func NewField(name string) *Field {
	return &Field{name, make([]string, 0)}
}

type Section struct {
	Note   string
	URL    string
	Fields []*Field
	Body   []string
}

func NewSection(note, url string) (*Section, error) {
	if note == "" && url == "" {
		return nil, errors.New("note or url must be given")
	} else if note != "" && url != "" {
		return nil, errors.New("only a note or a url can be given")
	}
	return &Section{note, url, make([]*Field, 0), make([]string, 0)}, nil
}

// TODO: make better?
func ContentTitle(doc *goquery.Document) string {
	title := RemoveExtraWhitespace(doc.Find("title").Text())
	return title
}

// TODO: make better?
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

// TODO: make better?
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

// TODO: make better?
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

// TODO: make better?
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

func (s *Section) AddTags(tags []string) {
	for _, tag := range tags {
		s.AddTag(tag)
	}
}

// TODO check if field exists?
func (s *Section) AddField(name string, values []string) {
	s.Fields = append(s.Fields, &Field{name, values})
}

func (s *Section) AppendBody(line string) {
	s.Body = append(s.Body, line)
}

func (s *Section) DeleteAll() {
	s.DeleteAllFields()
	s.DeleteBody()
}

func (s *Section) DeleteAllFields() {
	s.Fields = make([]*Field, 0)
}

func (s *Section) DeleteBody() {
	s.Body = make([]string, 0)
}

func (s *Section) DeleteField(name string) {
	s.Fields = slices.DeleteFunc(s.Fields, func(f *Field) bool { return f.Name == name })
}

func (s *Section) DeleteFields(names ...string) {
	for _, name := range names {
		s.DeleteField(name)
	}
}

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

func (s *Section) DeleteTags(tags []string) {
	for _, tag := range tags {
		s.DeleteTag(tag)
	}
}

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

func (s *Section) Field(name string) (*Field, bool) {
	for _, field := range s.Fields {
		if name == field.Name {
			return field, true
		}
	}
	return nil, false
}

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

func (s *Section) FieldHasValue(name string, values []string) bool {
	if len(values) == 0 {
		return true
	}
	fieldValues, ok := s.FieldValues("tags")
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

func (s *Section) FieldHasValues(name string, values []string) bool {
	if len(values) == 0 {
		return true
	}
	fieldValues, ok := s.FieldValues("tags")
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

func (s *Section) FieldValues(name string) ([]string, bool) {
	for _, field := range s.Fields {
		if name == field.Name {
			return field.Values, true
		}
	}
	return []string{}, false
}

func (s *Section) FillBody(lines []string) {
	if len(s.Body) == 0 {
		s.Body = lines
	}
}

func (s *Section) FillDate() {
	_, ok := s.Field("date")
	if !ok {
		s.SetDate()
	}
}

func (s *Section) FillFieldValue(name string, value string) {
	_, ok := s.Field(name)
	if !ok {
		s.SetFieldValue(name, value)
	}
}

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

func (s *Section) SetBody(lines []string) {
	s.Body = lines
}

func (s *Section) SetDate() {
	date := time.Now().Format(time.DateOnly)
	s.SetFieldValue("date", date)
}

func (s *Section) SetError(err error) {
	s.SetFieldValue("error", err.Error())
	s.DeleteField("status")
}

func (s *Section) SetField(name string, values []string) {
	field, ok := s.Field(name)
	if ok {
		field.Values = values
	} else {
		s.AddField(name, values)
	}
}

func (s *Section) SetFieldString(name string, value string) {
	if value == "" {
		s.DeleteField(name)
	} else {
		s.SetField(name, []string{value})
	}
}

func (s *Section) SetFieldValue(name string, value string) {
	s.SetField(name, []string{value})
}

func (s *Section) SetFieldValues(name string, values []string) {
	s.SetField(name, values)
}

func (s *Section) SetStatus(status string) {
	s.DeleteField("error")
	s.SetFieldValue("status", status)
}

func (s *Section) SetTags(tags []string) {
	if len(tags) == 0 {
		s.DeleteField("tags")
	} else {
		s.SetFieldValues("tags", tags)
	}
}

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

type WebNote struct {
	FilePath string
	Sections []*Section
}

func NewWebNote(filePath string) *WebNote {
	return &WebNote{filePath, make([]*Section, 0)}
}

func (wn *WebNote) AddSection(section *Section) {
	wn.Sections = append(wn.Sections, section)
}

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
			section, err = NewSection(line[len("# note://"):], "")
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

func errorWithLineNumber(err error, lineNumber int) error {
	return errors.New(err.Error() + " on line " + strconv.Itoa(lineNumber))
}

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

type NameWebNote struct {
	Name     string
	WebNote_ *WebNote
}

type IndexEntry struct {
	Name string
	MD5  string
}

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
	indexDirs := []string{"authors", "hosts", "tags"}
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
	tags := make(map[string]*NameWebNote)
	for _, filePath := range files {
		wn, err := LoadWebNote(filePath)
		if err != nil {
			return err
		}
		for _, sct := range wn.Sections {
			if sct.URL != "" {
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
	filePath = filepath.Join(IndexPath, "tags", "index")
	if err := SaveIndexFile(filePath, tags); err != nil {
		return err
	}
	return nil
}

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

func NameFromIndex(index []*IndexEntry, md5_ string) (string, error) {
	for _, ie := range index {
		if ie.MD5 == md5_ {
			return ie.Name, nil
		}
	}
	return "", errors.New("Unable to find index name")
}

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

// TODO: check for valid tags and remove space and stuff?
func GetTags(tagsString string) ([]string, error) {
	if len(tagsString) == 0 {
		return make([]string, 0), nil
	}
	return strings.Split(tagsString, ","), nil
}

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

func RemoveExtraWhitespace(text string) string {
	re := regexp.MustCompile(`\s+`)
	return strings.TrimSpace(re.ReplaceAllLiteralString(text, " "))
}
