package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/log"
	gap "github.com/muesli/go-app-paths"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

var (
	ConfigFile string
)

const DefaultConfig = `# style name or JSON path (default "auto")
style: "auto"
# mouse support (TUI-mode only)
mouse: false
# use pager to display markdown
pager: false
# word-wrap at width
width: 80
# show all files, including hidden and ignored.
all: true
`

// If no UI elements at all, defines default search parameters working
// for any instances
const (
	DEFAULT_SEARCH_Q      = "*"
	DEFAULT_SEARCH_FIELDS = "_extra,path"
	DEFAULT_SEARCH_SORT   = "-_extra.uploaded,_extra.id"
	DEFAULT_SEARCH_LATEST = true
	DEFAULT_SEARCH_SIZE   = 10
)

// Config contains TUI-specific configuration.

type Config struct {
	ShowAllFiles     bool
	ShowLineNumbers  bool
	Gopath           string `env:"GOPATH"`
	HomeDir          string `env:"HOME"`
	GlamourMaxWidth  uint
	GlamourStyle     string `env:"GLAMOUR_STYLE"`
	EnableMouse      bool
	PreserveNewLines bool

	// ArtifactDB instance info
	RootURL             string
	DefaultSearchParams SearchParams
	InstanceName        string

	// Open a specific document on startup (artifact ID).
	// If set, the TUI opens directly in the pager for this document.
	InitialDocument string

	// For debugging the UI
	HighPerformancePager bool `env:"ADB_HIGH_PERFORMANCE_PAGER" envDefault:"true"`
	GlamourEnabled       bool `env:"ADB_ENABLE_GLAMOUR"         envDefault:"true"`

	// ServeMode hides terminal-only UI elements (output dir, force download)
	// when running inside exo serve for the web app.
	ServeMode bool
}

type FieldProfile struct {
	Name  string `mapstructure:"name"`
	Value string `mapstructure:"value"`
}

type SearchFields struct {
	Profiles []FieldProfile
}

type StringProfileConfig struct {
	Name  string `mapstructure:"string"`
	Value string `mapstructure:"value"`
}

type BoolProfileConfig struct {
	Name  string `mapstructure:"string"`
	Value bool   `mapstructure:"value"`
}

type IntProfileConfig struct {
	Name  string `mapstructure:"string"`
	Value int    `mapstructure:"value"`
}

type SearchConfig struct {
	Query  []StringProfileConfig `mapstructure:"query"`
	Fields []StringProfileConfig `mapstructure:"fields"`
	Sort   []StringProfileConfig `mapstructure:"sort"`
	Latest []BoolProfileConfig   `mapstructure:"latest"`
	Size   []IntProfileConfig    `mapstructure:"size"`
}

type ActiveSearchConfig struct {
	Query  StringProfileConfig
	Fields StringProfileConfig
	Sort   StringProfileConfig
	Latest BoolProfileConfig
	Size   IntProfileConfig
}

func (asc *ActiveSearchConfig) SetFrom(search SearchUIConfig) {
	if len(search.Size) > 0 {
		asc.Size.Name = search.Size[0].Name
		if size, err := strconv.Atoi(search.Size[0].Name); err != nil {
			asc.Size.Value = size
		}
	}
	if len(search.Fields) > 0 {
		asc.Fields.Name = search.Fields[0].Name
		asc.Fields.Value = search.Fields[0].Value
	}
	if len(search.Sort) > 0 {
		asc.Sort.Name = search.Sort[0].Name
		asc.Sort.Value = search.Sort[0].Value
	}
	if len(search.Query) > 0 {
		asc.Query.Name = search.Query[0].Name
		asc.Query.Value = search.Query[0].Value
	}
	if len(search.Latest) > 0 {
		asc.Latest.Name = search.Latest[0].Name
		lowerStr := strings.ToLower(strings.TrimSpace(search.Latest[0].Value))
		var latest bool = true
		switch lowerStr {
		case "false", "0":
			latest = false
		}
		asc.Latest.Value = latest
	}
}

func (asc ActiveSearchConfig) Init() {
	// Hopefully some sensible default that would work in all instances
	asc.Query = StringProfileConfig{
		Name:  "*",
		Value: "*",
	}
	asc.Fields = StringProfileConfig{
		Name:  "*",
		Value: "_extra,path",
	}
	asc.Sort = StringProfileConfig{
		Name:  "*",
		Value: "-_extra.meta_indexed",
	}
	asc.Size = IntProfileConfig{
		Name:  "*",
		Value: 10,
	}
}

type ActiveConfig struct {
	Name     string
	Item     ItemUIConfig
	Artifact ArtifactUIConfig
	Search   ActiveSearchConfig
}

// Select the appropriate viewers given a URL and a _extra from a artifact.
func GetUIConfig(extra *Extra) *ActiveConfig {
	// Reload/merge base config with UI config
	TryLoadConfigFromDefaultPlaces()

	var config UIConfig
	err := viper.UnmarshalKey("ui", &config)
	if err != nil {
		log.Errorf("Error marshalling fields: %v", err)
		log.Fatal("fatal error")
	}

	var activeConfig ActiveConfig

	// Search
	var activeSearchConfig ActiveSearchConfig
	activeSearchConfig.Init()
	activeSearchConfig.SetFrom(config.Search)
	activeConfig.Search = activeSearchConfig

	// Item
	var defaultItem = ItemUIConfig{
		//Date: "{{- index (split $.Extra.Uploaded \"+\") 0 -}}",
		Date:        "{{ $.Extra.Uploaded.Format \"2006-01-02 15:04\" }}",
		Description: "{{ index (split $.Extra.Schema \"/\") 0 }}",
		Link:        "",
		Icon:        "",
		Title:       "{{ $.Extra.ID }}",
		Project:     "{{ $.Extra.ProjectID }}",
		Version:     "{{ $.Extra.Version }}",
		Path:        "",
		Access:      "{{- if eq $.Extra.Permissions.ReadAccess \"public\" -}}🌍{{- else if eq $.Extra.Permissions.ReadAccess \"none\" -}}🙈{{- else -}}🔒{{- end -}}",
	}
	var specificItem ItemUIConfig
	// Select item definition from template that matches the schema the document refers to
	// if none found, use default
	for _, item := range config.Views.Items {
		if strings.HasPrefix(extra.Schema, item.Schema) {
			specificItem = item
		}
		if item.Schema == "*" {
			defaultItem = item
		}
	}
	var activeItem ItemUIConfig
	if reflect.DeepEqual(specificItem, ItemUIConfig{}) {
		activeItem = defaultItem
	} else {
		activeItem = specificItem
	}
	activeConfig.Item = activeItem

	// Set fields from template if they exist, otherwise fallback to defaults
	fields := []string{"Icon", "Title", "Date", "Description", "Author", "Link", "Project", "Version", "Path", "Access"}
	for _, param := range fields {
		item := reflect.ValueOf(&activeConfig.Item).Elem()
		field := item.FieldByName(param)
		if !field.IsValid() {
			continue
		}
		if field.String() == "" {
			defaultIt := reflect.ValueOf(&defaultItem).Elem()
			defaultField := defaultIt.FieldByName(param)
			if !defaultField.IsValid() {
				continue
			}
			field.SetString(defaultField.String())
		}
	}

	// Artifact
	var defaultArtifact ArtifactUIConfig
	var specificArtifact ArtifactUIConfig
	// Same for artifact, select by schema
	for _, artifact := range config.Views.Artifacts {
		if strings.HasPrefix(extra.Schema, artifact.Schema) {
			specificArtifact = artifact
		}
		if artifact.Schema == "*" {
			defaultArtifact = artifact
		}
	}
	var activeArtifact ArtifactUIConfig
	if reflect.DeepEqual(specificArtifact, ArtifactUIConfig{}) {
		activeArtifact = defaultArtifact
	} else {
		activeArtifact = specificArtifact
	}
	activeConfig.Artifact = activeArtifact

	return &activeConfig
}

func TryLoadConfigFromDefaultPlaces() {
	uiConfigProvider.InitConfig()
	scope := gap.NewScope(gap.User, "adb")
	dirs, err := scope.ConfigDirs()
	if err != nil {
		log.Error("Could not find configuration directory.")
		log.Fatal("fatal error")
	}

	if c := os.Getenv("XDG_CONFIG_HOME"); c != "" {
		dirs = append([]string{filepath.Join(c, "adb")}, dirs...)
	}

	if c := os.Getenv("ADB_CONFIG_HOME"); c != "" {
		dirs = append([]string{c}, dirs...)
	}

	// Also search in the provider's config directory (e.g. exo's browse dir)
	if mainCfg := uiConfigProvider.GetMainConfigFile(); mainCfg != "" {
		dirs = append([]string{filepath.Dir(mainCfg)}, dirs...)
	}

	viper.SetConfigName("cli")
	for _, v := range dirs {
		viper.AddConfigPath(v)
	}

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			log.Warn("Could not parse configuration file", "err", err)
		} else {
			log.Debug("Config file not found", "dirs", dirs)
		}
	}

	// Even if no base config was found, try to load context-specific UI config
	used := viper.ConfigFileUsed()
	if used == "" {
		// Use the provider's config dir as fallback base
		if mainCfg := uiConfigProvider.GetMainConfigFile(); mainCfg != "" {
			used = mainCfg
		}
	}
	if used != "" {
		// We have a working config file, used for general usage of the CLI
		// Let's load additional context-specific config elements
		ctx, err := contextProvider.GetContext()
		if err == nil {
			configDir := filepath.Dir(used)
			uiConfigDir := filepath.Join(configDir, "contexts", ctx.Id, "ui")
			err = os.MkdirAll(uiConfigDir, os.ModePerm)
			if err != nil {
				log.Error("Failed to create ui directory", "err", err)
				log.Fatal("fatal error")
			}
			viper.AddConfigPath(uiConfigDir)
			viper.SetConfigName("cli")
			viper.SetConfigType("yaml")
			viper.SetEnvPrefix("adb")
			viper.AutomaticEnv()
			viper.MergeInConfig()
			//log.Error(viper.AllKeys())
		}

		return
	}

	if err := EnsureConfigFile(); err != nil {
		log.Error("Could not create default configuration", "error", err)
	}
	log.Debug("cfgFile", "file", ConfigFile)
}

func EnsureConfigFile() error {
	if ConfigFile == "" {
		ConfigFile = viper.GetViper().ConfigFileUsed()
		if ConfigFile == "" {
			// No config file configured or found — nothing to ensure
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(ConfigFile), 0o755); err != nil {
			return fmt.Errorf("could not write configuration file: %w", err)
		}
	}

	if ext := path.Ext(ConfigFile); ext != ".yaml" {
		return fmt.Errorf("'%s' is not a supported configuration type: use '%s'", ext, ".yaml")
	}

	if _, err := os.Stat(ConfigFile); errors.Is(err, fs.ErrNotExist) {
		// File doesn't exist yet, create all necessary directories and
		// write the default config file
		if err := os.MkdirAll(filepath.Dir(ConfigFile), 0o700); err != nil {
			return err
		}

		f, err := os.Create(ConfigFile)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()

		if _, err := f.WriteString(DefaultConfig); err != nil {
			return err
		}
	} else if err != nil { // some other error occurred
		return err
	}
	return nil
}

func GetConfigDir() string {
	// Once the whole the config was loaded, this can be used
	return filepath.Dir(uiConfigProvider.GetMainConfigFile())
}

// Return base folder "context", containing all context's specific information
func GetContextsDir() string {
	configDir := GetConfigDir()
	return filepath.Join(configDir, "contexts")
}

// Return the "ui" directory path, give a context
func GetContextualUIDir(ctx *Context) string {
	contextsDir := GetContextsDir()
	uiConfigDir := filepath.Join(contextsDir, ctx.Id, "ui")

	return uiConfigDir
}

// Return the "history" directory path, give a context (specific/local to a context)
func GetContextualHistoryDir(ctx *Context) string {
	contextsDir := GetContextsDir()
	historyConfigDir := filepath.Join(contextsDir, ctx.Id, "history")

	err := os.MkdirAll(historyConfigDir, os.ModePerm)
	if err != nil {
		log.Error("Failed to create history directory", "err", err)
		log.Fatal("fatal error")
	}

	return historyConfigDir
}

func downloadCLIConfig(ctx *Context) (*UICLIConfig, error) {
	ctxUrl := strings.TrimRight(ctx.Url, "/")
	uiUrl := ctxUrl + "/ui"
	var types UITypes
	response := contextProvider.MakeRequest("GET", uiUrl, nil, map[string]string{})
	if response.Err != nil {
		return nil, fmt.Errorf("Unable to fetch UI types from /ui endpoint: %s", response.Err)
	} else {
		response.AsJSON(&types)
	}

	if types.CLI.Path == "" {
		return nil, fmt.Errorf("Unable to obtain UI configuration for type CLI")
	}

	uiCLIUrl := strings.TrimRight(ctx.Url, "/") + types.CLI.Path
	var cliConfig UICLIConfig
	response = contextProvider.MakeRequest("GET", uiCLIUrl, nil, map[string]string{})
	if response.Err != nil {
		return nil, fmt.Errorf("Unable to fetch CLI configuration, path: %s, err: %s", types.CLI.Path, response.Err)
	}

	err := yaml.Unmarshal(response.Body, &cliConfig)
	if err != nil {
		return nil, fmt.Errorf("Failed to unmarshal YAML, err: %s", err)
	}

	return &cliConfig, nil
}

func DownloadUIConfig(autoConfirm ...bool) error {
	auto := len(autoConfirm) > 0 && autoConfirm[0]

	if !auto {
		var confirmResponse bool
		form := huh.NewGroup(
			huh.NewConfirm().
				Title("Download UI elements").
				Description("Do you want to download templates and UI elements (recommended)? This will overwrite existing ones.").
				Affirmative("Yes").
				Negative("No").
				Value(&confirmResponse),
		)
		huh.NewForm(form).Run()

		if !confirmResponse {
			log.Info("Abort...")
			return nil
		}
	}

	ctx := contextProvider.GetContextOrDie()
	cliConfig, err := downloadCLIConfig(ctx)
	if err != nil {
		log.Error("Error:", "err", err)
		log.Fatal("fatal error")
	}

	uiConfigDir := GetContextualUIDir(ctx)

	ctxUrl := strings.TrimRight(ctx.Url, "/")
	// CLI config contains template_path, which is related to the instance URL, we want to
	// convert that into an absolute path on the filesystem, as `template_file`, which is
	// what the CLI is reading from
	for idx := range cliConfig.UI.Views.Artifacts {
		// TODO: this one is kind of hardcoded: /ui/cli/, we requested /ui, than /ui/cli.yaml
		// but nothing tells use about /ui/cli/, for now, it's assumed...
		artifactViewer := cliConfig.UI.Views.Artifacts[idx]
		tplFile := strings.TrimPrefix(artifactViewer.TemplatePath, "/ui/cli/")
		artifactViewer.TemplateFile = filepath.Join(uiConfigDir, tplFile)
		DownloadArtifactViewerTemplate(ctxUrl, artifactViewer)
		cliConfig.UI.Views.Artifacts[idx] = artifactViewer
	}
	yamlContent, err := yaml.Marshal(cliConfig)
	if err != nil {
		return fmt.Errorf("Failed to marshal config struct to YAML, err: %s", err)
	}

	outFile := uiConfigDir + "/cli.yaml"
	if err := os.MkdirAll(uiConfigDir, 0755); err != nil {
		return fmt.Errorf("Failed to create UI config directory: %s", err)
	}
	err = os.WriteFile(outFile, yamlContent, 0644)
	if err != nil {
		log.Error("Failed to save UI configuration", "file", outFile, "err", err)
		log.Fatal("fatal error")
	}
	log.Info("Saved UI configuration", "file", outFile)

	return nil
}

func GetSearchHistoryFilenameForContext() (string, error) {
	ctx := contextProvider.GetContextOrDie()
	historyDir := GetContextualHistoryDir(ctx)
	filename := filepath.Join(historyDir, "search_q")
	// create one if none already
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		file, err := os.Create(filename)
		if err != nil {
			return "", err
		}
		defer file.Close()
	} else if err != nil {
		// Handle other errors (e.g., permission issues)
		return "", err
	}

	return filename, nil

}

func LoadSearchHistory() ([]string, error) {
	filename, err := GetSearchHistoryFilenameForContext()
	if err != nil {
		log.Error("Unable to create search history file", "err", err)
		return nil, err
	}
	file, err := os.Open(filename)
	if err != nil {
		log.Error("Unable to open search history file", "err", err)
		return []string{}, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return lines, nil
}

func AddToHistory(command string) error {
	filename, err := GetSearchHistoryFilenameForContext()
	if err != nil {
		return err
	}
	lines, err := LoadSearchHistory()
	lines = append(lines, command)
	lines = RemoveHistoryDuplicates(lines, 500) // like default bash history
	err = SaveHistory(filename, lines)

	return err
}

func RemoveHistoryDuplicates(lines []string, maxSize int) []string {
	uniqueLines := make(map[string]struct{})
	var result []string

	for _, line := range lines {
		if _, exists := uniqueLines[line]; !exists {
			uniqueLines[line] = struct{}{}
			result = append(result, line)
			if len(result) > maxSize {
				delete(uniqueLines, result[0])
				result = result[1:]
			}
		}
	}

	return result
}

func SaveHistory(filename string, lines []string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, line := range lines {
		_, err := writer.WriteString(line + "\n")
		if err != nil {
			return err
		}
	}

	return writer.Flush()
}

func DownloadArtifactViewerTemplate(ctxUrl string, artifactViewer ArtifactUIConfig) {
	response := contextProvider.MakeRequest("GET", ctxUrl+artifactViewer.TemplatePath, nil, map[string]string{})
	if response.Err != nil {
		log.Error("Unable to fetch template for artifact viewer", "schema", artifactViewer.Schema, "err", response.Err)
		log.Fatal("fatal error")
	}
	var err error
	templateDir := filepath.Dir(artifactViewer.TemplateFile)
	err = os.MkdirAll(templateDir, os.ModePerm)
	if err != nil {
		log.Error("Failed to create directory to save template file", "template_file", artifactViewer.TemplateFile, "err", err)
		log.Fatal("fatal error")
	}
	err = os.WriteFile(artifactViewer.TemplateFile, response.Body, 0644)
	if err != nil {
		log.Error("Failed to save artifact viewer template file", "file", artifactViewer.TemplateFile, "err", err)
		log.Fatal("fatal error")
	}
	log.Info("Saved artifact viewer template", "schema", artifactViewer.Schema, "file", artifactViewer.TemplateFile)
}

func NeedsUIElements() (hasUI bool) {
	ctx := contextProvider.GetContextOrDie()
	uiConfigDir := GetContextualUIDir(ctx)

	// don't bother continuing if no UI elements on the instance side
	if !hasRemoteUIElements() {
		return false
	}
	hasUI, err := hasLocalUIElements(uiConfigDir)
	if err != nil {
		log.Error("Unable to determine presence of UI elements", "err", err)
		log.Fatal("fatal error")
	}

	return !hasUI
}

func hasRemoteUIElements() bool {
	ctx := contextProvider.GetContextOrDie()
	elems, _ := downloadCLIConfig(ctx)

	return elems != nil
}

func hasLocalUIElements(dir string) (bool, error) {
	// Check if we can get UI elements from instance,
	// and if so, check if locally we already have something.
	contents, err := os.ReadDir(dir)

	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // Dir doesn't exist yet — no local elements
		}
		return false, err
	}
	return len(contents) != 0, nil
}

const DefaultTemplate = "# {{ ._extra.id | escapeMD }}\n\n```yaml\n{{ . | toYAML }}\n```\n"
