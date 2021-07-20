package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	gq "github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var (
	ErrInvalidURL      = errors.New("invalid url")
	ErrNoChaptersFound = errors.New("no chapters found in index")
	ErrParsingPage     = errors.New("error parsing page")
	ErrBookNotFound    = errors.New("book not found")
)

const (
	colRed    = "\033[31;1m"
	colYellow = "\033[33;1m"
	colReset  = "\033[m"
)

func usage(arg0 string, exitStatus int) {
	fmt.Fprintln(os.Stderr, `Usage:
  `+arg0+` [options...] <BOOK_URL>

Book URL format:
  http[s]://[www.]projekt-gutenberg.org/<author>/<book>[/whateverdoesntmatter]

Options:
  -dir <DIRECTORY>  --  Output directory (default: ".").

Output types:
  * <INFO>
  `+colYellow+`! <WARNING>`+colReset+`
  `+colRed+`! <ERROR>`+colReset)
	os.Exit(exitStatus)
}

func clearLine() {
	fmt.Print("\033[2K")
}

func printInfo(f string, v ...interface{}) {
	fmt.Printf("* "+f+"\n", v...)
}

func printWarn(f string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, colYellow+"! "+f+colReset+"\n", v...)
}

func printErr(f string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, colRed+"! "+f+colReset+"\n", v...)
	os.Exit(1)
}

func getBaseUrl(rawurl string) (string, error) {
	url, err := url.Parse(rawurl)
	if err != nil {
		return "", err
	}
	if !(url.Scheme == "http" || url.Scheme == "https") {
		return "", ErrInvalidURL
	}
	if !(url.Host == "projekt-gutenberg.org" || url.Host == "www.projekt-gutenberg.org") {
		return "", ErrInvalidURL
	}
	spPath := strings.Split(strings.Trim(url.Path, "/"), "/")
	if len(spPath) < 2 {
		return "", ErrInvalidURL
	}
	basePath := strings.Join(spPath[:2], "/")
	return url.Scheme + "://projekt-gutenberg.org/" + basePath, nil
}

// Returns a slice containing the links to the chapters.
func getChapters(baseUrl string, doc *gq.Document) ([]string, error) {
	chapterUrls := make([]string, 0, 8)
	doc.Find("body ul li").Each(func(i int, s *gq.Selection) {
		// The website has a strange bug where the 'a' element is separate from
		// the text element. That's why we have to search the entire 'li'
		// element for an 'a' element with a link.
		s = s.Find("a[href]")
		if len(s.Nodes) == 0 {
			// This should really never happen, that's why we're using panic.
			panic("missing link in chapter index")
		}
		relUrl, _ := s.Attr("href") // We now know it must have the href attribute.
		chapterUrls = append(chapterUrls, baseUrl+"/"+relUrl)
	})
	if len(chapterUrls) == 0 {
		return nil, ErrNoChaptersFound
	}
	return chapterUrls, nil
}

type MetaInfo struct {
	Author string
	Title  string
	Year   string
}

func getMetaInfo(doc *gq.Document) MetaInfo {
	metas := doc.Find("head meta")
	return MetaInfo{
		Author: metas.Filter("[name=\"author\"]").AttrOr("content", "Unknown"),
		Title:  metas.Filter("[name=\"title\"]").AttrOr("content", "Unknown"),
		Year:   metas.Filter("[name=\"firstpub\"]").AttrOr("content", "Unknown"),
	}
}

func (m MetaInfo) ToTitle() string {
	return fmt.Sprintf("%s -- %s, %s", m.Author, m.Title, m.Year)
}

type Extractor struct {
	BaseUrl     string
	Meta        MetaInfo
	ChapterUrls []string
	W           io.Writer
}

func NewExtractor(rawurl string, w io.Writer) (*Extractor, error) {
	baseUrl, err := getBaseUrl(rawurl)
	if err != nil {
		return nil, err
	}
	return &Extractor{
		BaseUrl: baseUrl,
		W:       w,
	}, nil
}

func (e *Extractor) FetchAndProcessIndex() error {
	// Get HTML document.
	resp, err := http.Get(e.BaseUrl)
	if err != nil {
		return err
	}
	if resp.StatusCode == 404 {
		return ErrBookNotFound
	}
	defer resp.Body.Close()
	// Parse HTML via Goquery.
	doc, err := gq.NewDocumentFromReader(resp.Body)
	if err != nil {
		return err
	}
	// Get metadata.
	metaInfo := getMetaInfo(doc)
	e.Meta = metaInfo
	// Get chapter URLs from index.
	chapterUrls, err := getChapters(e.BaseUrl, doc)
	if err != nil {
		return err
	}
	e.ChapterUrls = chapterUrls
	return nil
}

func (e *Extractor) parseAdditionalPage(doc *gq.Document) error {
	// Every document has two main <hr> elements with the given properties.
	// They are a way to mark the contained text.
	var passedHrs int
	var err error
	content := doc.Find("body").Children().FilterFunction(func(i int, s *gq.Selection) bool {
		if s.Is("hr[size=\"1\"][color=\"#808080\"]") {
			passedHrs++
			return false
		} else if s.Is("a") && (s.Text() == "<<\u00A0zurück" || s.Text() == "weiter\u00A0>>") {
			// We don't want the "zurück"/"weiter"-buttons
			return false
		}
		switch passedHrs {
		case 0:
			return false
		case 1:
			return true
		case 2:
			return false
		default:
			err = ErrParsingPage
			return false
		}
	})
	if err != nil {
		return err
	}

	// Now that we've extracted the actual content, convert it into markdown.
	var process func(*html.Node) string
	process = func(n *html.Node) string {
		processChildren := func() string {
			var ret string
			for i := n.FirstChild; i != nil; i = i.NextSibling {
				ret += process(i)
			}
			return ret
		}

		// Checks if `n` has the given HTML class.
		hasClass := func(class string) bool {
			for _, v := range n.Attr {
				if v.Key == "class" {
					classes := strings.Split(v.Val, " ")
					for _, cl := range classes {
						if cl == class {
							return true
						}
					}
					return false
				}
			}
			return false
		}

		var ret string
		switch n.Type {
		case html.TextNode:
			// If we have a text node, return the actual text after some
			// post-processing.
			ret = strings.ReplaceAll(n.Data, "\n", "")
			var newRet string
			// Replace all sequences of spaces consisting of more than one space
			// with just one space.
			var prevWasSpace bool
			for _, c := range ret {
				if c == ' ' {
					if prevWasSpace {
						continue
					}
					prevWasSpace = true
				} else {
					prevWasSpace = false
				}
				newRet += string(c)
			}
			ret = newRet
		case html.ElementNode:
			// Transform the individual HTML elements.
			switch n.DataAtom {
			case atom.Br:
				ret = "\n\n"
			case atom.H1:
				ret = "# " + processChildren() + "\n"
			case atom.H2:
				ret = "## " + processChildren() + "\n"
			case atom.H3:
				ret = "### " + processChildren() + "\n"
			case atom.H4:
				ret = "#### " + processChildren() + "\n"
			case atom.H5:
				ret = "##### " + processChildren() + "\n"
			case atom.H6:
				ret = "###### " + processChildren() + "\n"
			case atom.P:
				if hasClass("centerbig") {
					ret = "#### " + processChildren() + "\n\n"
				} else {
					ret = /*"    " + */ processChildren() + "\n\n"
				}
			case atom.Div:
				ret = processChildren()
			case atom.Tt:
				ret = "`" + processChildren() + "`"
			case atom.I:
				ret = "_" + processChildren() + "_"
			case atom.A:
				ret = processChildren()
			case atom.Span:
				ret = processChildren()
			case atom.Img:
			default:
				clearLine()
				printWarn("Unknown data atom: %v", n.Data)
			}
			// Add some CSS effects.
			if hasClass("spaced") {
				// Add spaced effect.
				var newRet string
				var runes []rune = []rune(ret)
				var nRunes = len(runes)
				for i := 0; i < nRunes; i++ {
					newRet += string(runes[i])
					if i < nRunes-1 {
						newRet += " "
					}
				}
				ret = newRet
			}
		default:
			clearLine()
			printWarn("Unknown type: %v", n.Type)
		}
		return ret
	}
	for _, n := range content.Nodes {
		fmt.Fprint(e.W, process(n))
	}
	return nil
}

func (e *Extractor) FetchAndProcessChapter(chapterUrl string) error {
	// Get HTML document.
	resp, err := http.Get(chapterUrl)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Parse HTML via Goquery (or really x/net/html).
	doc, err := gq.NewDocumentFromReader(resp.Body)
	if err != nil {
		return err
	}
	// Parse page.
	err = e.parseAdditionalPage(doc)
	if err != nil {
		return err
	}
	// Add horizontal rule after title page.
	if path.Base(chapterUrl) == "titlepage.html" {
		fmt.Fprintln(e.W, "\n----------------\n")
	}
	return nil
}

func main() {
	var url string
	dir := "."

	if len(os.Args) < 2 {
		usage(os.Args[0], 1)
	}

	// Parse command line arguments.
	for i := 1; i < len(os.Args); i++ {
		// Returns the argument after the given option. Errors if there is no
		// argument.
		expectArg := func(currArg string) string {
			i++
			if i >= len(os.Args) {
				printErr("Expected argument after option '%v'", currArg)
			}
			return os.Args[i]
		}

		arg := os.Args[i]
		if len(arg) >= 1 && arg[0] == '-' {
			switch arg {
			case "-dir":
				dir = expectArg(arg)
			case "--help", "-h":
				usage(os.Args[0], 0)
			default:
				printErr("Unknown option: '%v'", arg)
			}
		} else {
			if url == "" {
				url = arg
			} else {
				printErr("Expected option, but got '%v'", arg)
			}
		}
	}
	if url == "" {
		printInfo("Please specify a book URL")
		os.Exit(1)
	}
	printInfo("Book URL: %v", url)

	// Initial scraping.
	var b bytes.Buffer
	e, err := NewExtractor(url, &b)
	if err != nil {
		printErr("Error: %v", err)
	}
	err = e.FetchAndProcessIndex()
	if err != nil {
		printErr("Error: %v", err)
	}
	bookName := e.Meta.ToTitle()
	printInfo("Book: %v", bookName)

	// Download the actual chapters.
	for i, chapter := range e.ChapterUrls {
		clearLine()
		fmt.Printf("* Downloading chapter %v/%v...\r", i+1, len(e.ChapterUrls))
		err = e.FetchAndProcessChapter(chapter)
		if err != nil {
			printErr("Error: %v", err)
		}
	}

	// Write the generated markdown text to a file.
	filename := path.Join(dir, bookName+".md")
	os.WriteFile(filename, b.Bytes(), 0666)
	clearLine()
	printInfo("Saved as: %v", filename)
}
