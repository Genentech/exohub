package tui

import (
	"bytes"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"text/template"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"

	"github.com/Genentech/exohub/go/exo/palette"
	"github.com/muesli/reflow/truncate"
	"github.com/sahilm/fuzzy"
)

const (
	verticalLine         = "│"
	fileListingStashIcon = "• "
)

func artifactItemView(b *strings.Builder, m stashModel, index int, md *markdown, absoluteIndex int) {
	truncateTo := uint(m.common.width - stashViewHorizontalPadding*2)
	uiConfig := GetUIConfig(&md.Extra)
	item := templateItem(&uiConfig.Item, &md.Result)

	// ensure description/title is on one line otherwise it messes up the display
	re := regexp.MustCompile(`[\r\n]+`)
	// set defaults, valid for all adb instances
	title := strings.TrimSpace(re.ReplaceAllString(item.Title, " "))
	title = truncate.StringWithTail(title, truncateTo, ellipsis)
	description := strings.TrimSpace(re.ReplaceAllString(item.Description, " "))
	description = truncate.StringWithTail(description, truncateTo/2, ellipsis)
	date := truncate.StringWithTail(item.Date, 24, ellipsis)
	link := item.Link
	access := strings.TrimSpace(item.Access)

	// Build project@version badge with distinct colors per part
	project := strings.TrimSpace(item.Project)
	version := strings.TrimSpace(item.Version)
	var projectBadge string
	if project != "" {
		projectBadge = renderProjectBadge(project, version)
	}
	var (
		gutter    string
		separator = ""
		icon      = ""
	)

	if item.Icon != "" {
		icon = fmt.Sprintf("%s ", item.Icon)
	}

	isSelected := index == m.cursor()
	isMarked := m.selected[absoluteIndex]

	if isMarked {
		// Inverse/negative style: light text on dark background
		hlStyle := lipgloss.NewStyle().
			Background(palette.Current().Error.Adaptive()).
			Foreground(palette.Current().AccentDim.Adaptive())
		if isSelected {
			gutter = fuchsiaFg(verticalLine)
		} else {
			gutter = " "
		}
		line1 := fmt.Sprintf("%s %s", gutter, hlStyle.Render(fmt.Sprintf("%s%s ", icon, title)))
		line2Parts := []string{gutter}
		if access != "" {
			line2Parts = append(line2Parts, access)
		}
		if projectBadge != "" {
			line2Parts = append(line2Parts, projectBadge)
		}
		line2Parts = append(line2Parts, grayFg(date), midGrayFg(description))
		if link != "" {
			line2Parts = append(line2Parts, midGrayFg(link))
		}
		fmt.Fprintf(b, "%s\n%s", line1, strings.Join(line2Parts, " "))
		return
	}

	link = midGrayFg(link)
	description = midGrayFg(description)
	date = grayFg(date)

	if isSelected {
		gutter = fuchsiaFg(verticalLine)
		icon = fuchsiaFg(icon)
		title = fuchsiaFg(title)
		separator = dimDullFuchsiaFg(separator)
	} else {
		gutter = " "
		icon = dimFuchsiaFg(icon)
		title = dimFuchsiaFg(title)
		separator = brightGrayFg(separator)
	}

	fmt.Fprintf(b, "%s %s%s%s%s\n", gutter, icon, separator, separator, title)
	line2Parts := []string{gutter}
	if access != "" {
		line2Parts = append(line2Parts, access)
	}
	if projectBadge != "" {
		line2Parts = append(line2Parts, projectBadge)
	}
	line2Parts = append(line2Parts, date, description)
	if link != "" {
		line2Parts = append(line2Parts, link)
	}
	fmt.Fprint(b, strings.Join(line2Parts, " "))
}

func scrollItemView(b *strings.Builder, m stashModel, index int, md *markdown) {
	var (
		gutter    = " "
		icon      = ""
		separator = ""
		title     string
	)
	// Check if this is an error scroll
	if md.ScrollError != "" {
		log.Debug("Rendering error scroll item")
		title = "Scroll expired, pagination not possible anymore"
		icon = "⚠️ "
	} else {
		title = "Select to load more results..."
		icon = "📄 "
	}

	isSelected := index == m.cursor()

	if md.ScrollError != "" {
		// Red style for error
		if isSelected {
			gutter = redFg(verticalLine)
			icon = redFg(icon)
			title = redFg(title)
		} else {
			icon = redFg(icon)
			title = redFg(title)
		}
	} else {
		// Normal style for loading more
		if isSelected {
			gutter = fuchsiaFg(verticalLine)
			icon = fuchsiaFg(icon)
			title = fuchsiaFg(title)
		} else {
			gutter = " "
			icon = dimFuchsiaFg(icon)
			title = dimFuchsiaFg(title)
		}
	}

	separator = brightGrayFg(separator)
	fmt.Fprintf(b, "%s %s%s%s%s\n", gutter, icon, separator, separator, title)
}

func styleFilteredText(haystack, needles string, defaultStyle, matchedStyle lipgloss.Style) string {
	b := strings.Builder{}

	normalizedHay, err := normalize(haystack)
	if err != nil {
		log.Error("error normalizing", "haystack", haystack, "error", err)
	}

	matches := fuzzy.Find(needles, []string{normalizedHay})
	if len(matches) == 0 {
		return defaultStyle.Render(haystack)
	}

	m := matches[0] // only one match exists
	for i, rune := range []rune(haystack) {
		styled := false
		for _, mi := range m.MatchedIndexes {
			if i == mi {
				b.WriteString(matchedStyle.Render(string(rune)))
				styled = true
			}
		}
		if !styled {
			b.WriteString(defaultStyle.Render(string(rune)))
		}
	}

	return b.String()
}

func templateItem(itemConfig *ItemUIConfig, result *SearchResult) *ItemUIConfig {

	var item ItemUIConfig
	cfg := reflect.ValueOf(*itemConfig)
	itemReflect := reflect.ValueOf(&item).Elem()

	if cfg.Kind() == reflect.Struct {
		// Iterate over each field
		for i := 0; i < cfg.NumField(); i++ {
			// Get the field name and value
			fieldName := cfg.Type().Field(i).Name
			templateString := cfg.Field(i)
			// Print the field name and value
			value := templateField(templateString.String(), result)
			field := itemReflect.FieldByName(fieldName)
			valueReflect := reflect.ValueOf(value)
			if field.IsValid() && field.CanSet() {
				field.SetString(valueReflect.String())
			} else {
				log.Error("Error setting templated field", "field", field, "value", value)
			}
		}
	}

	//var link string
	//if ids, err := utils.ParseArtifactIdentifier(extra.ID); err == nil {
	//	link = ids.Path
	//}

	// We return an ItemConfig struct as the templated result, even it's not
	// a config element, it's just config and templated item have the same fields.
	return &item
}

func templateField(templateString string, result *SearchResult) string {
	// Create and parse the template
	tmpl, err := template.New("item").Funcs(
		template.FuncMap{
			"getFieldByJSONTag": GetFieldByJSONTagPath,
			"toJSON":            ToJSON,
			"isMap":             IsMap,
			"isSlice":           IsSlice,
			"join":              JoinElements,
			"int":               ConvertInt,
			"format":            FormatNumber,
			"split":             Split,
			"humanSize":         HumanSize,
		}).Parse(templateString)
	if err != nil {
		log.Error("Error parsing template", "err", err)
		return templateString
	}

	// Create a buffer to capture the output
	var output bytes.Buffer

	// Execute the template with the result map
	if err := tmpl.Execute(&output, result); err != nil {
		log.Error("Error executing template", "err", err)
		return templateString
	}

	return output.String()
}
