package palette

// Patsica is an intentionally garish theme for stress-testing the palette system.
var Patsica = Palette{
	Name:        "patsica",
	Description: "Chaotic eyesore (for testing purposes only)",

	Accent:     ColorPair{Light: "#FF00FF", Dark: "#00FF00"},
	AccentDim:  ColorPair{Light: "#FF00FF", Dark: "#00FF00"},
	AccentDull: ColorPair{Light: "#FFFF00", Dark: "#FF00FF"},

	Success:    ColorPair{Light: "#0000FF", Dark: "#FF0000"},
	SuccessDim: ColorPair{Light: "#0000FF", Dark: "#FF0000"},

	Error:   ColorPair{Light: "#00FF00", Dark: "#FFFF00"},
	Warning: ColorPair{Light: "#FF0000", Dark: "#00FFFF"},

	Highlight: ColorPair{Light: "#FF00FF", Dark: "#FFFF00"},

	Label:    ColorPair{Light: "#00FFFF", Dark: "#FF00FF"},
	LabelAlt: ColorPair{Light: "#FFFF00", Dark: "#00FFFF"},

	Dim: ColorPair{Light: "#FF0000", Dark: "#0000FF"},

	Badge: ColorPair{Light: "#00FF00", Dark: "#FF00FF"},

	ProgressFilled: ColorPair{Light: "#FF00FF", Dark: "#00FF00"},
	ProgressEmpty:  ColorPair{Light: "#00FF00", Dark: "#FF00FF"},

	CodeFg: ColorPair{Light: "#FF0000", Dark: "#00FF00"},
	CodeBg: ColorPair{Light: "#0000FF", Dark: "#FF00FF"},

	LogoBg: ColorPair{Light: "#FFFF00", Dark: "#0000FF"},
	LogoFg: ColorPair{Light: "#FF00FF", Dark: "#FFFF00"},

	StatusBarFg: ColorPair{Light: "#00FF00", Dark: "#FF0000"},
	StatusBarBg: ColorPair{Light: "#FF00FF", Dark: "#00FF00"},

	SearchHighlightFg: ColorPair{Light: "#0000FF", Dark: "#FF00FF"},
	SearchHighlightBg: ColorPair{Light: "#00FF00", Dark: "#FFFF00"},
}
