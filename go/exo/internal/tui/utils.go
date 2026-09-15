package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/dustin/go-humanize"
	"gopkg.in/yaml.v2"

	"github.com/Genentech/exohub/go/exo/palette"
)

// RemoveFrontmatter removes the front matter header of a markdown file.
func RemoveFrontmatter(content []byte) []byte {
	if frontmatterBoundaries := detectFrontmatter(content); frontmatterBoundaries[0] == 0 {
		return content[frontmatterBoundaries[1]:]
	}
	return content
}

var yamlPattern = regexp.MustCompile(`(?m)^---\r?\n(\s*\r?\n)?`)

func detectFrontmatter(c []byte) []int {
	if matches := yamlPattern.FindAllIndex(c, 2); len(matches) > 1 {
		return []int{matches[0][0], matches[1][1]}
	}
	return []int{-1, -1}
}

// WrapCodeBlock wraps a string in a code block with the given language.
func WrapCodeBlock(s, language string) string {
	return "```" + language + "\n" + s + "```"
}

var markdownExtensions = []string{
	".md", ".mdown", ".mkdn", ".mkd", ".markdown",
}

// IsMarkdownFile returns whether the filename has a markdown extension.
func IsMarkdownFile(filename string) bool {
	ext := filepath.Ext(filename)

	if ext == "" {
		return true
	}

	for _, v := range markdownExtensions {
		if strings.EqualFold(ext, v) {
			return true
		}
	}

	return false
}

// GlamourStyle returns a glamour TermRendererOption for the given style.
func GlamourStyle(style string, isCode bool) glamour.TermRendererOption {
	var styleConfig ansi.StyleConfig

	switch style {
	case styles.AutoStyle:
		if lipgloss.HasDarkBackground() {
			styleConfig = styles.DarkStyleConfig
		} else {
			styleConfig = styles.LightStyleConfig
		}
	case styles.DarkStyle:
		styleConfig = styles.DarkStyleConfig
	case styles.LightStyle:
		styleConfig = styles.LightStyleConfig
	case styles.PinkStyle:
		styleConfig = styles.PinkStyleConfig
	case styles.NoTTYStyle:
		styleConfig = styles.NoTTYStyleConfig
	case styles.DraculaStyle:
		styleConfig = styles.DraculaStyleConfig
	case styles.TokyoNightStyle:
		styleConfig = styles.TokyoNightStyleConfig
	default:
		return glamour.WithStylesFromJSONFile(style)
	}

	var margin uint
	styleConfig.CodeBlock.Margin = &margin

	pp := palette.Current()
	crossed := false
	bgcolor := pp.SearchHighlightBg.Dark
	color := pp.SearchHighlightFg.Dark
	if palette.CurrentMode() == palette.ModeLight {
		bgcolor = pp.SearchHighlightBg.Light
		color = pp.SearchHighlightFg.Light
	}
	if themeHighlightBg != "" {
		bgcolor = themeHighlightBg
	}
	if themeHighlightFg != "" {
		color = themeHighlightFg
	}
	bold := true
	styleConfig.Strikethrough.CrossedOut = &crossed
	styleConfig.Strikethrough.BackgroundColor = &bgcolor
	styleConfig.Strikethrough.Color = &color
	styleConfig.Strikethrough.Bold = &bold

	// Apply palette-derived colors to glamour markdown rendering
	resolvePP := func(cp palette.ColorPair) string {
		if palette.CurrentMode() == palette.ModeLight {
			return cp.Light
		}
		return cp.Dark
	}

	primaryColor := resolvePP(pp.Accent)
	successColor := resolvePP(pp.Success)
	labelColor := resolvePP(pp.Label)
	dimColor := resolvePP(pp.Dim)

	h1Fg := resolvePP(pp.LogoFg)
	styleConfig.H1.Color = &h1Fg
	styleConfig.H1.BackgroundColor = &primaryColor

	styleConfig.H2.Color = &primaryColor
	h2Bold := true
	styleConfig.H2.Bold = &h2Bold

	styleConfig.H3.Color = &labelColor
	styleConfig.H4.Color = &labelColor
	styleConfig.H5.Color = &labelColor
	styleConfig.H6.Color = &labelColor

	styleConfig.Link.Color = &primaryColor
	styleConfig.LinkText.Color = &primaryColor

	blockQuoteColor := resolvePP(pp.Warning)
	styleConfig.BlockQuote.Color = &blockQuoteColor
	styleConfig.Item.Color = &successColor
	styleConfig.Emph.Color = &successColor

	// Code block styling
	codeFgColor := resolvePP(pp.CodeFg)
	codeBgColor := resolvePP(pp.CodeBg)
	styleConfig.Code.Color = &codeFgColor
	styleConfig.Code.BackgroundColor = &codeBgColor

	if styleConfig.CodeBlock.Chroma != nil {
		styleConfig.CodeBlock.Chroma.NameTag = ansi.StylePrimitive{Color: &successColor}
		styleConfig.CodeBlock.Chroma.NameAttribute = ansi.StylePrimitive{Color: &successColor}
		styleConfig.CodeBlock.Chroma.Keyword = ansi.StylePrimitive{Color: &primaryColor}
		styleConfig.CodeBlock.Chroma.KeywordReserved = ansi.StylePrimitive{Color: &primaryColor}
		styleConfig.CodeBlock.Chroma.KeywordType = ansi.StylePrimitive{Color: &primaryColor}
		highlightColor := resolvePP(pp.Highlight)
		styleConfig.CodeBlock.Chroma.LiteralNumber = ansi.StylePrimitive{Color: &highlightColor}
		styleConfig.CodeBlock.Chroma.LiteralString = ansi.StylePrimitive{Color: &dimColor}
	}

	return glamour.WithStyles(styleConfig)
}

// SanitizeFields ensures _extra is always in the fields list.
func SanitizeFields(fields string) string {
	parts := strings.Split(fields, ",")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}
	const extra = "_extra"
	if !slices.Contains(parts, extra) {
		parts = append(parts, extra)
	}
	return strings.Join(parts, ",")
}

// GetFieldByJSONTagPath accesses fields using dot-separated JSON tag paths.
func GetFieldByJSONTagPath(v interface{}, path string) interface{} {
	parts := strings.Split(path, ".")
	current := reflect.ValueOf(v)

	for _, part := range parts {
		if !current.IsValid() {
			return nil
		}

		if current.Kind() == reflect.Struct {
			found := false
			for i := 0; i < current.NumField(); i++ {
				field := current.Type().Field(i)
				tag := field.Tag.Get("json")
				tagParts := strings.Split(tag, ",")
				if tagParts[0] == part {
					current = current.Field(i)
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}
	}

	return current.Interface()
}

// ToJSON returns a pretty-printed JSON string.
func ToJSON(v interface{}) string {
	jsonData, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Error("Unable to marshal", "err", err)
	}
	return string(jsonData)
}

// HighlightTerms wraps occurrences of terms in strikethrough markers.
func HighlightTerms(text string, terms []interface{}) string {
	var buf bytes.Buffer
	for _, term := range terms {
		if strTerm, ok := term.(string); ok {
			highlightedTerm := fmt.Sprintf("~~%s~~", strTerm)
			text = strings.ReplaceAll(text, strTerm, highlightedTerm)
		} else {
			log.Error("non-string element found in terms slice")
		}
	}
	buf.WriteString(text)
	return buf.String()
}

// HighlightTermsInSlice applies highlighting across a slice of text items.
func HighlightTermsInSlice(text []interface{}, terms []interface{}) []string {
	var highlightedTexts []string
	for _, item := range text {
		if strItem, ok := item.(string); ok {
			highlightedText := HighlightTerms(strItem, terms)
			if highlightedText == "" {
				highlightedText = strItem
			}
			highlightedTexts = append(highlightedTexts, highlightedText)
		} else {
			log.Error("non-string element found in terms slice")
		}
	}
	return highlightedTexts
}

// ansiEscapeRegex matches ANSI escape sequences.
var ansiEscapeRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// highlightTermsInANSI applies colored highlighting to search terms in ANSI-rendered text.
// It splits the text into ANSI escape sequences and plain text segments, then
// case-insensitively replaces term occurrences in plain text with ANSI-colored versions.
// This preserves existing formatting and works safely inside code blocks.
func highlightTermsInANSI(rendered string, terms []string) string {
	if len(terms) == 0 {
		return rendered
	}

	// ANSI highlight: yellow background (48;5;226), black foreground (38;5;0), bold (1)
	hlStart := "\x1b[1;38;5;0;48;5;226m"
	hlEnd := "\x1b[0m"

	// Process each line independently to avoid cross-line ANSI state issues
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		// Split the line into segments: alternating plain text and ANSI sequences
		indices := ansiEscapeRegex.FindAllStringIndex(line, -1)
		if len(indices) == 0 {
			// No ANSI codes — highlight the whole line
			for _, term := range terms {
				line = caseInsensitiveReplace(line, term, hlStart, hlEnd)
			}
			lines[i] = line
			continue
		}

		// Build result from segments
		var result strings.Builder
		pos := 0
		for _, idx := range indices {
			// Plain text before this ANSI sequence
			if pos < idx[0] {
				plain := line[pos:idx[0]]
				for _, term := range terms {
					plain = caseInsensitiveReplace(plain, term, hlStart, hlEnd)
				}
				result.WriteString(plain)
			}
			// ANSI sequence — pass through unchanged
			result.WriteString(line[idx[0]:idx[1]])
			pos = idx[1]
		}
		// Trailing plain text
		if pos < len(line) {
			plain := line[pos:]
			for _, term := range terms {
				plain = caseInsensitiveReplace(plain, term, hlStart, hlEnd)
			}
			result.WriteString(plain)
		}
		lines[i] = result.String()
	}
	return strings.Join(lines, "\n")
}

// caseInsensitiveReplace replaces all case-insensitive occurrences of term in text
// with ANSI-colored versions using raw escape codes.
func caseInsensitiveReplace(text, term, hlStart, hlEnd string) string {
	lower := strings.ToLower(text)
	lowerTerm := strings.ToLower(term)
	termLen := len(lowerTerm)

	var result strings.Builder
	pos := 0
	for {
		idx := strings.Index(lower[pos:], lowerTerm)
		if idx == -1 {
			result.WriteString(text[pos:])
			break
		}
		result.WriteString(text[pos : pos+idx])
		result.WriteString(hlStart)
		result.WriteString(text[pos+idx : pos+idx+termLen])
		result.WriteString(hlEnd)
		pos += idx + termLen
	}
	return result.String()
}

// IsMap checks if the value is a map[string]string.
func IsMap(content interface{}) bool {
	_, ok := content.(map[string]string)
	return ok
}

// IsSlice checks if the value is a non-empty slice ([]string or []interface{}).
func IsSlice(content interface{}) bool {
	if _, ok := content.([]string); ok {
		return true
	}
	if arr, ok := content.([]interface{}); ok {
		return len(arr) > 0
	}
	return false
}

// Split splits a string by a separator.
func Split(s string, sep string) []string {
	return strings.Split(s, sep)
}

// HumanSize formats a byte count as a human-readable string (e.g. "28 kB").
func HumanSize(value interface{}) string {
	switch v := value.(type) {
	case int:
		return humanize.Bytes(uint64(v))
	case int64:
		return humanize.Bytes(uint64(v))
	case FlexibleInt64:
		return humanize.Bytes(uint64(v))
	case float64:
		return humanize.Bytes(uint64(v))
	default:
		return fmt.Sprintf("%v", v)
	}
}

// FormatNumber formats a number for display.
func FormatNumber(value interface{}) string {
	switch v := value.(type) {
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case FlexibleInt64:
		return fmt.Sprintf("%d", int64(v))
	case float64:
		return fmt.Sprintf("%.2f", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ConvertInt converts a value to int.
func ConvertInt(value interface{}) int {
	var asInt int
	var err error
	switch v := value.(type) {
	case int:
		asInt = v
	case int64:
		asInt = int(v)
	case FlexibleInt64:
		asInt = int(v)
	case float64:
		asInt = int(v)
	case string:
		asInt, err = strconv.Atoi(v)
		if err != nil {
			asInt = 0
		}
	default:
		asInt = 0
	}
	return asInt
}

// JoinElements joins a list of interface{} values with a separator.
func JoinElements(list []interface{}, separator string, trim bool) string {
	var result strings.Builder
	for i, elem := range list {
		switch v := elem.(type) {
		case string:
			if trim {
				v = strings.Trim(v, "\r\n")
			}
			result.WriteString(v)
		default:
			result.WriteString(fmt.Sprintf("%v", v))
		}
		if i < len(list)-1 {
			result.WriteString(separator)
		}
	}
	return result.String()
}

// GetTemplateFuncMap returns the template function map for rendering.
func GetTemplateFuncMap() TemplateFuncMap {
	return TemplateFuncMap{
		"highlightTerms":        HighlightTerms,
		"highlightTermsInSlice": HighlightTermsInSlice,
		"toJSON":                ToJSON,
		"isMap":                 IsMap,
		"isSlice":               IsSlice,
		"join":                  JoinElements,
		"int":                   ConvertInt,
		"format":                FormatNumber,
		"split":                 Split,
		"humanSize":             HumanSize,
		"toYAML":                DisplayJSONAsYAML,
		"escapeMD":              EscapeMarkdown,
	}
}

// EscapeMarkdown escapes characters that goldmark would interpret as
// inline formatting or autolinks. Uses backslash escapes for CommonMark-
// supported punctuation and a zero-width space to break email autolinks
// on @ which CommonMark does not allow backslash-escaping.
func EscapeMarkdown(s interface{}) string {
	str := fmt.Sprintf("%v", s)
	r := strings.NewReplacer(
		`_`, `\_`,
		`@`, "@​",
		`<`, `\<`,
		`>`, `\>`,
	)
	return r.Replace(str)
}

// TemplateFuncMap is an alias for template.FuncMap to avoid importing text/template.
type TemplateFuncMap = map[string]interface{}

// DisplayJSONAsYAML converts a JSON document to YAML format.
func DisplayJSONAsYAML(document map[string]interface{}) (string, error) {
	yamlData, err := yaml.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("error converting JSON to YAML: %v", err)
	}
	return string(yamlData), nil
}
