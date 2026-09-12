package palette

// Mono is a grayscale theme for accessibility and theme-system verification.
// All roles use shades of gray, with red for errors and yellow for warnings
// to maintain critical visibility.
var Mono = Palette{
	Name:        "mono",
	Description: "Black, white, and grayscale",

	Accent:    ColorPair{Light: "#000000", Dark: "#FFFFFF"},
	AccentDim: ColorPair{Light: "#444444", Dark: "#BBBBBB"},
	AccentDull: ColorPair{Light: "#666666", Dark: "#888888"},

	Success:    ColorPair{Light: "#333333", Dark: "#CCCCCC"},
	SuccessDim: ColorPair{Light: "#555555", Dark: "#999999"},

	Error:   ColorPair{Light: "#CC0000", Dark: "#FF6666"},
	Warning: ColorPair{Light: "#AA8800", Dark: "#FFCC00"},

	Highlight: ColorPair{Light: "#000000", Dark: "#FFFFFF"},

	Label:    ColorPair{Light: "#222222", Dark: "#DDDDDD"},
	LabelAlt: ColorPair{Light: "#555555", Dark: "#AAAAAA"},

	Dim: ColorPair{Light: "#999999", Dark: "#777777"},

	Badge: ColorPair{Light: "#444444", Dark: "#CCCCCC"},

	ProgressFilled: ColorPair{Light: "#000000", Dark: "#FFFFFF"},
	ProgressEmpty:  ColorPair{Light: "#AAAAAA", Dark: "#555555"},

	CodeFg: ColorPair{Light: "#333333", Dark: "#CCCCCC"},
	CodeBg: ColorPair{Light: "#E8E8E8", Dark: "#2A2A2A"},

	LogoBg: ColorPair{Light: "#333333", Dark: "#DDDDDD"},
	LogoFg: ColorPair{Light: "#FFFFFF", Dark: "#000000"},

	StatusBarFg: ColorPair{Light: "#333333", Dark: "#DDDDDD"},
	StatusBarBg: ColorPair{Light: "#DDDDDD", Dark: "#333333"},

	SearchHighlightFg: ColorPair{Light: "#000000", Dark: "#000000"},
	SearchHighlightBg: ColorPair{Light: "#CCCCCC", Dark: "#FFFFFF"},
}
