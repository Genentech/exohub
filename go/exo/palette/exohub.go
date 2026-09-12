package palette

// ExoHub is the default theme using the full ExoHub brand gradient
// (orange #FF6B35 → pink #E84393 → purple #9B59B6).
//
// The gradient is mapped across semantic roles:
//   - Orange end: Label, Warning, Highlight, ProgressFilled, Badge
//   - Pink center: Accent (primary), LogoBg, SearchHighlight
//   - Purple end: LabelAlt, AccentDull, ProgressEmpty
//   - Intermediate: AccentDim (rose), Dim (muted purple-gray)
//
// Dark mode uses bright/saturated variants on dark backgrounds.
// Light mode uses darker/more saturated variants on light backgrounds.
var ExoHub = Palette{
	Name:        "exohub",
	Description: "ExoHub brand gradient (orange / pink / purple)",

	// Pink center of gradient — primary selection/accent
	Accent:    ColorPair{Light: "#C4256E", Dark: "#E84393"},
	AccentDim: ColorPair{Light: "#A04878", Dark: "#D17EB8"}, // rose, between pink and purple
	AccentDull: ColorPair{Light: "#7B4380", Dark: "#B07CC0"}, // muted violet

	// Teal/cyan — cool complement to the warm gradient, extends the brand palette
	Success:    ColorPair{Light: "#0E7A6B", Dark: "#2DD4BF"},
	SuccessDim: ColorPair{Light: "#4A8A80", Dark: "#1A9E8E"},

	// Red/coral from the orange→pink transition
	Error:   ColorPair{Light: "#CC3344", Dark: "#FF6B6B"},
	Warning: ColorPair{Light: "#CC6B20", Dark: "#FFB347"}, // warm amber-orange

	// Yellow end of gradient — high-visibility active elements
	Highlight: ColorPair{Light: "#A68B09", Dark: "#ECFD65"},

	// Orange end of gradient — section headers
	Label:    ColorPair{Light: "#C45A1E", Dark: "#FF6B35"},
	// Purple end of gradient — secondary labels
	LabelAlt: ColorPair{Light: "#6B3A7A", Dark: "#B07CC0"},

	// Muted purple-gray from the gradient
	Dim: ColorPair{Light: "#7A6E88", Dark: "#8A7A9B"},

	// Deep coral-orange, between orange and pink
	Badge: ColorPair{Light: "#B8662E", Dark: "#F2884B"},

	// Gradient endpoints for progress
	ProgressFilled: ColorPair{Light: "#C45A1E", Dark: "#FF6B35"},
	ProgressEmpty:  ColorPair{Light: "#6B3A7A", Dark: "#9B59B6"},

	// Purple-tinted code blocks
	CodeFg: ColorPair{Light: "#5A4A6A", Dark: "#B8A8C8"},
	CodeBg: ColorPair{Light: "#F0ECF5", Dark: "#1E1A2E"},

	// Pink badge
	LogoBg: ColorPair{Light: "#E84393", Dark: "#E84393"},
	LogoFg: ColorPair{Light: "#FFFFFF", Dark: "#FFFFFF"},

	// Rose-tinted status bar
	StatusBarFg: ColorPair{Light: "#6B2F4A", Dark: "#FFB088"},
	StatusBarBg: ColorPair{Light: "#F5D5E0", Dark: "#6B2F4A"},

	// Pink search highlights
	SearchHighlightFg: ColorPair{Light: "#FFFFFF", Dark: "#FFFFFF"},
	SearchHighlightBg: ColorPair{Light: "#C4256E", Dark: "#E84393"},
}
