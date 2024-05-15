package main

import (
	"crypto/md5"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/greglange/webnotes/pkg/webnotes"
)

// TODO: command line flag to specify a root directory instead of defaulting to current directory
// TODO: check if the wrong or unused options are specified for each main?
// TODO: add main append
// TODO: maybe add md body specifier that tries to change html to markdown

var mainFuncs = map[string]func(*options) error{
	"add":        mainAdd,
	"append":     mainAppend,
	"clear":      mainClear,
	"copy":       mainCopy,
	"delete":     mainDelete,
	"duplicates": mainDuplicates,
	"fill":       mainFill,
	"format":     mainFormat,
	"head":       mainHead,
	"http":       mainHttp,
	"index":      mainIndex,
	"matches":    mainMatches,
	"move":       mainMove,
	"set":        mainSet,
	"tag":        mainTag,
}

var boolSectionMatchers = []string{
	"note", "url",
}

var sectionMatchers = []string{
	"author", "body", "date", "description", "error", "host", "note", "status", "tags", "title", "url",
}

var boolValueSpecifiers = []string{
	"all", "author", "body", "date", "description", "error", "status", "tags", "title",
}

var boolBodySpecifiers = []string{
	"images", "links", "p", "text",
}

var getValueSpecifiers = []string{
	"images", "links", "p", "text", "title",
}

var stringValueSpecifiers = []string{
	"author", "body", "date", "description", "note", "tags", "title", "url",
}

// flags that specify section fields as a single string value
var valueSpecifiers = []string{"author", "date", "description", "title"}

type options struct {
	b map[string]bool
	s map[string]string
}

func getOptions() *options {
	b := map[string]*bool{}
	s := map[string]*string{}
	boolFlags := append(append(append([]string{"verbose"}, boolValueSpecifiers...), boolBodySpecifiers...), boolSectionMatchers...)
	stringFlags := []string{
		// file matchers
		"dir", "file",
		// others
		"out_file"}
	for f, _ := range mainFuncs {
		b[f] = flag.Bool(f, false, "")
	}
	for _, f := range boolFlags {
		b[f] = flag.Bool(f, false, "")
	}
	for _, sm := range sectionMatchers {
		e := "e" + sm
		s[e] = flag.String(e, "", "")
		m := "m" + sm
		s[m] = flag.String(m, "", "")
	}
	for _, f := range stringFlags {
		s[f] = flag.String(f, "", "")
	}
	for _, f := range stringValueSpecifiers {
		v := "v" + f
		s[v] = flag.String(v, "", "")
	}
	flag.Usage = usage
	flag.Parse()
	o := options{make(map[string]bool), make(map[string]string)}
	for k, v := range b {
		o.b[k] = *v
	}
	for k, v := range s {
		o.s[k] = *v
	}
	return &o
}

func (o *options) hasGetSpecifier() bool {
	for _, name := range getValueSpecifiers {
		if o.b[name] {
			return true
		}
	}
	return false
}

func (o *options) outWebNotesFile() (*webnotes.WebNote, error) {
	filePath := o.s["out_file"]
	if filePath == "" {
		return nil, errors.New("Must specify --out_file")
	}
	if !strings.HasSuffix(filePath, ".wn") {
		return nil, errors.New("Out file must end with .wn")
	}
	var wn *webnotes.WebNote
	exists, err := webnotes.FileExists(filePath)
	if err != nil {
		return nil, err
	}
	if exists {
		wn, err = webnotes.LoadWebNote(filePath)
		if err != nil {
			return nil, err
		}
	} else {
		wn = webnotes.NewWebNote(filePath)
	}
	return wn, nil
}

type fileMatcher struct {
	dir  string
	file string
}

func (o *options) fileMatcher() (*fileMatcher, error) {
	count := 0
	if o.s["dir"] != "" {
		count++
	}
	if o.s["file"] != "" {
		count++
	}
	if count > 1 {
		return nil, errors.New("Only one file matcher option can be specified")
	}
	return &fileMatcher{o.s["dir"], o.s["file"]}, nil
}

func (fm *fileMatcher) matches(fp string) (bool, error) {
	if fm.dir != "" {
		return path.Dir(fp) == fm.dir, nil
	} else if fm.file != "" {
		return fm.file == fp, nil
	} else {
		return true, nil
	}
}

func (o *options) matchingFiles() ([]string, error) {
	fm, err := o.fileMatcher()
	if err != nil {
		return nil, err
	}
	fps, err := webnotes.GetWebNoteFiles(".")
	if err != nil {
		return nil, err
	}
	files := []string{}
	for _, fp := range fps {
		matches, err := fm.matches(fp)
		if err != nil {
			return nil, err
		} else if matches {
			files = append(files, fp)
		}
	}
	return files, nil
}

type sectionMatcher struct {
	b     map[string]bool
	e     map[string]string
	m     map[string]*regexp.Regexp
	etags []string
	mtags []string
}

func (o *options) sectionMatcher() (*sectionMatcher, error) {
	sm := &sectionMatcher{map[string]bool{}, map[string]string{}, map[string]*regexp.Regexp{}, []string{}, []string{}}
	count := 0
	for _, name := range []string{"note", "url"} {
		if o.b[name] {
			sm.b[name] = true
			count++
		}
	}
	if count > 1 {
		return nil, errors.New("Only one of --note and --url can be specified")
	}
	for _, s := range sectionMatchers {
		count := 0
		if o.s["e"+s] != "" {
			sm.e[s] = o.s["e"+s]
			count++
		}
		if o.s["m"+s] != "" {
			regexp_, err := regexp.Compile(o.s["m"+s])
			if err != nil {
				return nil, err
			}
			sm.m[s] = regexp_
			count++
		}
		if count > 1 {
			return nil, errors.New(fmt.Sprintf("Only one of --%s and --%s can be specified", "e"+s, "m"+s))
		}
	}
	var err error
	sm.etags, err = webnotes.GetTags(o.s["etags"])
	if err != nil {
		return nil, err
	}
	sm.mtags, err = webnotes.GetTags(o.s["mtags"])
	if err != nil {
		return nil, err
	}
	return sm, nil
}

func (sm *sectionMatcher) matches(sct *webnotes.Section) bool {
	bools := true
	if sm.b["note"] {
		bools = bools && sct.Note != ""
	}
	if sm.b["url"] {
		bools = bools && sct.URL != ""
	}
	equals := true
	for name, value := range sm.e {
		if name == "body" {
			if len(sct.Body) != 1 {
				equals = false
			} else {
				equals = equals && sct.Body[0] == value
			}
		} else if name == "host" {
			equals = equals && sct.EqualsHost(value)
		} else if name == "note" {
			equals = equals && sct.Note == value
		} else if name == "tags" {
			// this is handled below
		} else if name == "url" {
			equals = equals && sct.URL == value
		} else {
			equals = equals && sct.FieldEqualsValue(name, value)
		}
	}
	matches := true
	for name, regexp_ := range sm.m {
		if name == "body" {
			matchesBody := false
			for _, line := range sct.Body {
				if regexp_.Match([]byte(line)) {
					matchesBody = true
					break
				}
			}
			matches = matches && matchesBody
		} else if name == "host" {
			host, err := sct.Host()
			if err == nil {
				// TODO: what about the error here?
				matches = matches && regexp_.Match([]byte(host))
			}
		} else if name == "note" {
			matches = matches && regexp_.Match([]byte(sct.Note))
		} else if name == "tags" {
			// this is handled below
		} else if name == "url" {
			matches = matches && regexp_.Match([]byte(sct.URL))
		} else {
			value, ok := sct.FieldValue(name)
			// TODO: what about the error here?
			if ok {
				matches = matches && regexp_.Match([]byte(value))
			} else {
				matches = false
			}
		}
	}
	tags := true
	if len(sm.etags) > 0 {
		tags = tags && sct.FieldHasValues("tags", sm.etags)
	}
	if len(sm.mtags) > 0 {
		tags = tags && sct.FieldHasValue("tags", sm.mtags)
	}
	return bools && equals && matches && tags
}

func (sm *sectionMatcher) matchingSections(filePath string) (*webnotes.WebNote, []int, error) {
	wn, err := webnotes.LoadWebNote(filePath)
	if err != nil {
		return nil, nil, err
	}
	indexes := []int{}
	for i, sct := range wn.Sections {
		if !sm.matches(sct) {
			continue
		}
		indexes = append(indexes, i)
	}
	return wn, indexes, nil
}

type httpHandler struct {
	o      *options
	index_ map[string][]*webnotes.IndexEntry
}

func newHttpHandler(o *options) (*httpHandler, error) {
	return &httpHandler{o, make(map[string][]*webnotes.IndexEntry)}, nil
}

func (h *httpHandler) index(name string) ([]*webnotes.IndexEntry, error) {
	index, ok := h.index_[name]
	if ok {
		return index, nil
	}
	filePath := filepath.Join(webnotes.IndexPath, name, "index")
	index, err := webnotes.LoadIndexFile(filePath)
	if err != nil {
		return nil, err
	}
	h.index_[name] = index
	return index, nil
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// this is supposed to prevent the browser from caching pages
	// https://stackoverflow.com/questions/69597242/golang-prevent-browser-cache-pages-when-cli
	w.Header().Set("Cache-Control", "no-cache, private, max-age=0")
	w.Header().Set("Expires", time.Unix(0, 0).Format(http.TimeFormat))
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Accel-Expires", "0")

	if r.URL.Path == "/" {
		h.pageMain(w)
	} else if r.URL.Path == "/authors" {
		h.pageIndex(w, "authors")
	} else if r.URL.Path == "/hosts" {
		h.pageIndex(w, "hosts")
	} else if r.URL.Path == "/files" {
		h.pageFiles(w)
	} else if r.URL.Path == "/tags" {
		h.pageIndex(w, "tags")
	} else {
		parts := strings.Split(r.URL.Path[1:], "/")
		if len(parts) < 2 {
			h.pageMessage(w, "Invalid url")
			return
		}
		if parts[0] == "file" {
			filePath := filepath.Join(parts[1:len(parts)]...)
			urlPath := fmt.Sprintf("/file/%s", filePath)
			h.pageFile(w, filePath, urlPath, "file: "+filePath)
		} else if parts[0] == "authors" {
			if len(parts) > 2 {
				h.pageMessage(w, "Invalid url")
			}
			h.pageIndexFile(w, "authors", parts[1])
		} else if parts[0] == "hosts" {
			if len(parts) > 2 {
				h.pageMessage(w, "Invalid url")
			}
			h.pageIndexFile(w, "hosts", parts[1])
		} else if parts[0] == "tags" {
			if len(parts) > 2 {
				h.pageMessage(w, "Invalid url")
			}
			h.pageIndexFile(w, "tags", parts[1])
		} else {
			h.pageMessage(w, "Invalid url")
		}
	}
}

func (h *httpHandler) pageError(w http.ResponseWriter, err error) {
	h.pageMessage(w, err.Error())
}

func (h *httpHandler) pageFile(w http.ResponseWriter, filePath, urlPath, msg string) {
	wn, err := webnotes.LoadWebNote(filePath)
	if err != nil {
		h.pageError(w, err)
		return
	}
	fmt.Fprintf(w, "<html><head></head><body>\n")
	fmt.Fprintf(w, "<a href=\"/\">main</a> | %s\n", msg)
	for _, sct := range wn.Sections {
		fmt.Fprintf(w, "<hr>\n")
		if sct.Note != "" {
			fmt.Fprintf(w, "<p><a id=\"%s\" href=\"%s#%s\">#</a> note://%s</a></p>\n", sct.Note, urlPath, sct.Note, sct.Note)
		} else if sct.URL != "" {
			md5_ := fmt.Sprintf("%x", md5.Sum([]byte(sct.URL)))
			fmt.Fprintf(w, "<p><a id=\"%s\" href=\"%s#%s\">#</a> <a href=\"%s\">%s</a></p>\n", md5_, urlPath, md5_, sct.URL, sct.URL)
		} else {
			// this should not happen with a well formed section
			fmt.Fprintf(w, "<p><a href=\"https://example.com\">https://example.com</a></p>\n")
		}
		for _, field := range sct.Fields {
			if field.Name == "tags" {
				tags := []string{}
				for _, tag := range field.Values {
					md5_ := fmt.Sprintf("%x", md5.Sum([]byte(tag)))
					tags = append(tags, fmt.Sprintf("<a href=\"/tags/%s\">%s</a>", md5_, tag))
				}
				fmt.Fprintf(w, "<p>tags: %s</p>\n", strings.Join(tags, ", "))
			} else {
				fmt.Fprintf(w, "<p>%s: %s</p>\n", field.Name, strings.Join(field.Values, ", "))
			}
		}
		if len(sct.Body) > 0 {
			fmt.Fprintf(w, "%s\n", webnotes.MarkdownToHTML(strings.Join(sct.Body, "\n")))
		}
	}
	fmt.Fprintf(w, "</body></html>")
}

func (h *httpHandler) pageFiles(w http.ResponseWriter) {
	files, err := webnotes.GetWebNoteFiles(".")
	if err != nil {
		h.pageError(w, err)
		return
	}
	fmt.Fprintf(w, "<html><head></head><body>\n")
	fmt.Fprintf(w, "<a href=\"/\">main</a> | files\n")
	fmt.Fprintf(w, "<hr>\n")
	for _, filePath := range files {
		parts := filepath.SplitList(filePath)
		file := strings.Join(parts, "/")
		fmt.Fprintf(w, "<p><a href=\"/file/%s\">%s</a></p>\n", file, file)
	}
	fmt.Fprintf(w, "</body></html>")
}

func (h *httpHandler) pageIndexFile(w http.ResponseWriter, indexName string, md5_ string) {
	indexEntries, err := h.index(indexName)
	if err != nil {
		h.pageError(w, err)
		return
	}
	name, err := webnotes.NameFromIndex(indexEntries, md5_)
	if err != nil {
		h.pageError(w, err)
		return
	}
	filePath := filepath.Join(webnotes.IndexPath, indexName, fmt.Sprintf("%s.wn", md5_))
	urlPath := fmt.Sprintf("/%s/%s", indexName, md5_)
	h.pageFile(w, filePath, urlPath, indexName[0:len(indexName)-1]+": "+name)
}

func (h *httpHandler) pageIndex(w http.ResponseWriter, indexName string) {
	indexEntries, err := h.index(indexName)
	if err != nil {
		h.pageError(w, err)
		return
	}
	fmt.Fprintf(w, "<html><head></head><body>\n")
	fmt.Fprintf(w, "<a href=\"/\">main</a> | %s\n", indexName)
	fmt.Fprintf(w, "<hr>")
	for _, ie := range indexEntries {
		fmt.Fprintf(w, "<p><a href=\"/%s/%s\">%s</a></p>\n", indexName, ie.MD5, ie.Name)
	}
	fmt.Fprintf(w, "</body></html>")
}

func (h *httpHandler) pageMain(w http.ResponseWriter) {
	fmt.Fprintf(w, "<html><head></head><body>\n")
	fmt.Fprintf(w, "<a href=\"/\">main</a> | main")
	fmt.Fprintf(w, "<hr>")
	fmt.Fprintf(w, "<p><a href=\"/authors\">authors</a></p>\n")
	fmt.Fprintf(w, "<p><a href=\"/hosts\">hosts</a></p>\n")
	fmt.Fprintf(w, "<p><a href=\"/files\">files</a></p>\n")
	fmt.Fprintf(w, "<p><a href=\"/tags\">tags</a></p>\n")
	fmt.Fprintf(w, "</body></html>")
}

func (h *httpHandler) pageMessage(w http.ResponseWriter, msg string) {
	fmt.Fprintf(w, "<html><head></head><body>\n")
	fmt.Fprintf(w, "<a href=\"/\">main</a> | %s\n", msg)
	fmt.Fprintf(w, "</body></html>\n")
}

func usage() {
	fmt.Println("Usage of webnotes:")
	fmt.Println(" main selectors:")
	fmt.Println("  These choose what the webnote command will do")
	fmt.Println("  --add : adds a webnote")
	fmt.Println("  --append : appends to webnotes' bodies")
	fmt.Println("  --clear : clears webnotes fields and/or bodies")
	fmt.Println("  --copy : copies webnotes to a different file")
	fmt.Println("  --delete : deletes webnotes")
	fmt.Println("  --duplicates : prints duplicate webnotes")
	fmt.Println("  --fill : sets webnotes fields and/or bodies if not already set")
	fmt.Println("  --format : loads webnote files and saves them standard formating")
	fmt.Println("  --head : does an HTTP head on webnotes")
	fmt.Println("  --http : runs a webserver so webnotes can be viewed in browser")
	fmt.Println("  --index : builds the index for a set of webnotes")
	fmt.Println("  --matches : prints webnotes that match comand line selectors")
	fmt.Println("  --move : moves webnotes to a different file")
	fmt.Println("  --set : sets webnotes fields and/or bodies")
	fmt.Println("  --tag : puts a tag on webnotes")
	fmt.Println(" file selectors:")
	fmt.Println("  These choose which files the webnote command will operate on.")
	fmt.Println("  Defaults to all files.")
	fmt.Println("  --dir <directory>")
	fmt.Println("  --file <file>")
	fmt.Println(" bool webnote selectors:")
	fmt.Println("  --note : matches notes")
	fmt.Println("  --url : matchers urls")
	fmt.Println(" string webnote selectors:")
	fmt.Println("  These select which webnotes to operate on.")
	fmt.Println("  e version for equals")
	fmt.Println("  m version for pattern matches")
	fmt.Println("  --eauthor, mauthor <string>: author field")
	fmt.Println("  --ebody, mbody <string>: body")
	fmt.Println("  --edate, mdate <string>: date field")
	fmt.Println("  --edescription, mdescription <string>: description field")
	fmt.Println("  --eerror, merror <string>: error field")
	fmt.Println("  --ehost, mhost <string>: host of url")
	fmt.Println("  --enote, mnote <string>: note string")
	fmt.Println("  --estatus, mstatus <string>: status field")
	fmt.Println("  --etags, mtags <string>: tags field")
	fmt.Println("  --etitle, mtitle <string>: title field")
	fmt.Println("  --eurl, murl <string>: url")
	fmt.Println(" boolean webnote selectors:")
	fmt.Println("  These specify the part of the webnote to operate on.")
	fmt.Println("  --all : all fields and body")
	fmt.Println("  --author : auhtor field")
	fmt.Println("  --body : body")
	fmt.Println("  --date : date field")
	fmt.Println("  --description : descrption field")
	fmt.Println("  --error : error field")
	fmt.Println("  --status : status field")
	fmt.Println("  --tags : tags field")
	fmt.Println("  --title : title field")
	fmt.Println(" body specifiers:")
	fmt.Println("  These specify how to grab the body of the webnote from the url.")
	fmt.Println("  --images : grab images from url and write as markdown")
	fmt.Println("  --links : grab links from url and write as markdown")
	fmt.Println("  --p : grab text inside of <p></p> tags")
	fmt.Println("  --text : grab all text from url")
	fmt.Println(" value specifiers:")
	fmt.Println("  These specify the value for the url, body, and fields")
	fmt.Println("  --vauthor <author of webnote>")
	fmt.Println("  --vbody <body of webnote>")
	fmt.Println("  --vdate <date of webnote>")
	fmt.Println("  --vdescription <description of webnote>")
	fmt.Println("  --vnote <note string>")
	fmt.Println("  --vtags <tags for webnote>")
	fmt.Println("  --vtitle <webnote title>")
	fmt.Println("  --vurl <webnote url>")
	fmt.Println(" output file specifier:")
	fmt.Println("  This specifies which file output is written to.")
	fmt.Println("  --out_file <file>")
}

func main() {
	code := 0
	defer func() {
		os.Exit(code)
	}()
	o := getOptions()
	var mainFunc func(*options) error
	for k, v := range mainFuncs {
		if o.b[k] {
			if mainFunc != nil {
				fmt.Println("Only one main option allowed")
				code = 1
				return
			}
			mainFunc = v
		}
	}
	if mainFunc == nil {
		fmt.Println("You must choose a main option")
		code = 1
		return
	} else {
		err := mainFunc(o)
		if err != nil {
			fmt.Println(err)
			code = 1
			return
		}
	}
}

func mainAdd(o *options) error {
	out, err := o.outWebNotesFile()
	if err != nil {
		return err
	}
	note := o.s["vnote"]
	// TODO: verify url is right format?
	url := o.s["vurl"]
	if note == "" && url == "" {
		return errors.New("Must specify --vnote or --vurl")
	} else if note != "" && url != "" {
		return errors.New("Can only specify one of -vnote and -vurl")
	}
	section, err := webnotes.NewSection(note, url)
	if err != nil {
		return err
	}
	if o.b["date"] {
		section.SetDate()
	}
	for _, name := range valueSpecifiers {
		v := "v" + name
		if o.s[v] != "" {
			section.SetFieldValue(name, o.s[v])
		}
	}
	if o.s["vbody"] != "" {
		section.SetBody([]string{o.s["vbody"]})
	}
	tags, err := webnotes.GetTags(o.s["vtags"])
	if err != nil {
		return err
	}
	section.SetTags(tags)
	if o.hasGetSpecifier() {
		if section.URL != "" {
			doc, err := section.Get()
			if err == nil {
				if o.b["images"] {
					section.SetBody(webnotes.ContentImages(doc))
				}
				if o.b["links"] {
					section.SetBody(webnotes.ContentLinks(doc))
				}
				if o.b["p"] {
					section.SetBody(webnotes.ContentP(doc))
				}
				if o.b["text"] {
					section.SetBody(webnotes.ContentText(doc))
				}
				if o.b["title"] {
					section.SetFieldValue("title", webnotes.ContentTitle(doc))
				}
			}
		}
	}
	out.AddSection(section)
	err = webnotes.SaveWebNote(out)
	if err != nil {
		return err
	}
	return nil
}

func mainAppend(o *options) error {
	// only works on the body
	// TODO: main
	fmt.Println("Not implemented")
	return nil
}

func mainClear(o *options) error {
	fps, err := o.matchingFiles()
	if err != nil {
		return err
	}
	sm, err := o.sectionMatcher()
	if err != nil {
		return err
	}
	for _, fp := range fps {
		wn, indexes, err := sm.matchingSections(fp)
		if err != nil {
			return err
		}
		for _, i := range indexes {
			for _, name := range boolValueSpecifiers {
				if name == "all" {
					if o.b["all"] {
						wn.Sections[i].DeleteAll()
					}
				} else if name == "body" {
					if o.b["body"] {
						wn.Sections[i].DeleteBody()
					}
				} else {
					if o.b[name] {
						wn.Sections[i].DeleteField(name)
					}
				}
			}
		}
		if len(indexes) > 0 {
			err = webnotes.SaveWebNote(wn)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func mainCopy(o *options) error {
	fps, err := o.matchingFiles()
	if err != nil {
		return err
	}
	out, err := o.outWebNotesFile()
	if err != nil {
		return err
	}
	sm, err := o.sectionMatcher()
	if err != nil {
		return err
	}
	for _, fp := range fps {
		wn, indexes, err := sm.matchingSections(fp)
		if err != nil {
			return err
		}
		for _, i := range indexes {
			out.AddSection(wn.Sections[i])
		}
	}
	err = webnotes.SaveWebNote(out)
	if err != nil {
		return err
	}
	return nil
}

func mainDelete(o *options) error {
	fps, err := o.matchingFiles()
	if err != nil {
		return err
	}
	sm, err := o.sectionMatcher()
	if err != nil {
		return err
	}
	for _, fp := range fps {
		wn, indexes, err := sm.matchingSections(fp)
		if err != nil {
			return err
		}
		for _, i := range indexes {
			wn.Sections[i] = nil
		}
		if len(indexes) > 0 {
			err = webnotes.SaveWebNote(wn)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func mainDuplicates(o *options) error {
	fps, err := o.matchingFiles()
	if err != nil {
		return err
	}
	sm, err := o.sectionMatcher()
	if err != nil {
		return err
	}
	ids := make(map[string][]string)
	for _, fp := range fps {
		wn, indexes, err := sm.matchingSections(fp)
		if err != nil {
			return err
		}
		for _, i := range indexes {
			id, err := wn.Sections[i].ID()
			if err != nil {
				return err
			}
			_, ok := ids[id]
			if ok {
				ids[id] = append(ids[id], fp)
			} else {
				ids[id] = []string{fp}
			}
		}
	}
	for id, files := range ids {
		if len(files) > 1 {
			fmt.Println(strings.Join(files, ",") + ": " + id)
		}
	}
	return nil
}

func mainFill(o *options) error {
	fps, err := o.matchingFiles()
	if err != nil {
		return err
	}
	sm, err := o.sectionMatcher()
	if err != nil {
		return err
	}
	for _, fp := range fps {
		wn, indexes, err := sm.matchingSections(fp)
		if err != nil {
			return err
		}
		for _, i := range indexes {
			if o.b["date"] {
				wn.Sections[i].FillDate()
			}
			for _, name := range valueSpecifiers {
				v := "v" + name
				if o.s[v] != "" {
					wn.Sections[i].FillFieldValue(name, o.s[v])
				}
			}
			if o.s["vbody"] != "" {
				wn.Sections[i].FillBody([]string{o.s["vbody"]})
			}
			if o.s["vtags"] != "" {
				tags, err := webnotes.GetTags(o.s["vtags"])
				if err != nil {
					return err
				}
				for _, tag := range tags {
					wn.Sections[i].AddTag(tag)
				}
			}
			if o.hasGetSpecifier() {
				if wn.Sections[i].URL != "" {
					doc, err := wn.Sections[i].Get()
					if err == nil {
						if o.b["images"] {
							wn.Sections[i].FillBody(webnotes.ContentImages(doc))
						}
						if o.b["links"] {
							wn.Sections[i].FillBody(webnotes.ContentLinks(doc))
						}
						if o.b["p"] {
							wn.Sections[i].FillBody(webnotes.ContentP(doc))
						}
						if o.b["text"] {
							wn.Sections[i].FillBody(webnotes.ContentText(doc))
						}
						if o.b["title"] {
							wn.Sections[i].FillFieldValue("title", webnotes.ContentTitle(doc))
						}
					}
				}
			}
		}
		if len(indexes) > 0 {
			err = webnotes.SaveWebNote(wn)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func mainFormat(o *options) error {
	fps, err := o.matchingFiles()
	if err != nil {
		return err
	}
	for _, fp := range fps {
		wn, err := webnotes.LoadWebNote(fp)
		if err != nil {
			return err
		}
		err = webnotes.SaveWebNote(wn)
		if err != nil {
			return err
		}
	}
	return nil
}

func mainHead(o *options) error {
	fps, err := o.matchingFiles()
	if err != nil {
		return err
	}
	sm, err := o.sectionMatcher()
	if err != nil {
		return err
	}
	for _, fp := range fps {
		wn, indexes, err := sm.matchingSections(fp)
		if err != nil {
			return err
		}
		for _, i := range indexes {
			if wn.Sections[i].URL != "" {
				wn.Sections[i].Head()
			}
		}
		if len(indexes) > 0 {
			err = webnotes.SaveWebNote(wn)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func mainHttp(o *options) error {
	httpHandler, err := newHttpHandler(o)
	if err != nil {
		return err
	}
	http.Handle("/", httpHandler)
	return http.ListenAndServe(":8080", nil)
}

func mainIndex(o *options) error {
	return webnotes.BuildIndex()
}

func mainMatches(o *options) error {
	fps, err := o.matchingFiles()
	if err != nil {
		return err
	}
	sm, err := o.sectionMatcher()
	if err != nil {
		return err
	}
	for _, fp := range fps {
		wn, indexes, err := sm.matchingSections(fp)
		if err != nil {
			return err
		}
		for _, i := range indexes {
			fmt.Println(wn.Sections[i])
		}
	}
	return nil
}

func mainMove(o *options) error {
	fps, err := o.matchingFiles()
	if err != nil {
		return err
	}
	out, err := o.outWebNotesFile()
	if err != nil {
		return err
	}
	sm, err := o.sectionMatcher()
	if err != nil {
		return err
	}
	for _, fp := range fps {
		wn, indexes, err := sm.matchingSections(fp)
		if err != nil {
			return err
		}
		for _, i := range indexes {
			out.AddSection(wn.Sections[i])
			wn.Sections[i] = nil
		}
		if len(indexes) > 0 {
			err = webnotes.SaveWebNote(wn)
			if err != nil {
				return err
			}
		}
	}
	err = webnotes.SaveWebNote(out)
	if err != nil {
		return err
	}
	return nil
}

func mainSet(o *options) error {
	fps, err := o.matchingFiles()
	if err != nil {
		return err
	}
	sm, err := o.sectionMatcher()
	if err != nil {
		return err
	}
	for _, fp := range fps {
		wn, indexes, err := sm.matchingSections(fp)
		if err != nil {
			return err
		}
		for _, i := range indexes {
			if o.b["date"] {
				wn.Sections[i].SetDate()
			}
			for _, name := range valueSpecifiers {
				v := "v" + name
				if o.s[v] != "" {
					wn.Sections[i].SetFieldValue(name, o.s[v])
				}
			}
			if o.s["vbody"] != "" {
				wn.Sections[i].SetBody([]string{o.s["vbody"]})
			}
			if o.s["vtags"] != "" {
				tags, err := webnotes.GetTags(o.s["vtags"])
				if err != nil {
					return err
				}
				wn.Sections[i].SetField("tags", tags)
			}
			if o.hasGetSpecifier() {
				if wn.Sections[i].URL != "" {
					doc, err := wn.Sections[i].Get()
					if err == nil {
						if o.b["images"] {
							wn.Sections[i].SetBody(webnotes.ContentImages(doc))
						}
						if o.b["links"] {
							wn.Sections[i].SetBody(webnotes.ContentLinks(doc))
						}
						if o.b["p"] {
							wn.Sections[i].SetBody(webnotes.ContentP(doc))
						}
						if o.b["text"] {
							wn.Sections[i].SetBody(webnotes.ContentText(doc))
						}
						if o.b["title"] {
							wn.Sections[i].SetFieldValue("title", webnotes.ContentTitle(doc))
						}
					}
				}
			}
		}
		if len(indexes) > 0 {
			err = webnotes.SaveWebNote(wn)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func mainTag(o *options) error {
	fps, err := o.matchingFiles()
	if err != nil {
		return err
	}
	sm, err := o.sectionMatcher()
	if err != nil {
		return err
	}
	for _, fp := range fps {
		wn, indexes, err := sm.matchingSections(fp)
		if err != nil {
			return err
		}
		for _, i := range indexes {
			tags, err := webnotes.GetTags(o.s["vtags"])
			if err != nil {
				return err
			}
			wn.Sections[i].AddTags(tags)
		}
		if len(indexes) > 0 {
			err = webnotes.SaveWebNote(wn)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
