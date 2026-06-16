package styles

import (
	"encoding/json"
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
)

// opencodeTheme is the minimal structure needed from an OpenCode theme JSON.
type opencodeTheme struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Dark struct {
		Palette struct {
			Neutral    string `json:"neutral"`
			Ink        string `json:"ink"`
			Primary    string `json:"primary"`
			Accent     string `json:"accent"`
			Success    string `json:"success"`
			Warning    string `json:"warning"`
			Error      string `json:"error"`
			Info       string `json:"info"`
			DiffAdd    string `json:"diffAdd"`
			DiffDelete string `json:"diffDelete"`
		} `json:"palette"`
	} `json:"dark"`
}

// ThemeFromJSON parses an OpenCode-format theme JSON and returns Crush Styles.
func ThemeFromJSON(data []byte) (Styles, error) {
	var t opencodeTheme
	if err := json.Unmarshal(data, &t); err != nil {
		return Styles{}, fmt.Errorf("parse theme json: %w", err)
	}
	p := t.Dark.Palette
	if p.Neutral == "" {
		return Styles{}, fmt.Errorf("theme %q missing dark palette", t.ID)
	}
	setDefault := func(field *string, def string) {
		if *field == "" {
			*field = def
		}
	}
	setDefault(&p.Accent, p.Primary)
	setDefault(&p.Info, p.Primary)

	opts := quickStyleOpts{
		primary:   lipgloss.Color(p.Primary),
		secondary: lipgloss.Color(p.Accent),
		accent:    lipgloss.Color(p.Accent),
		keyword:   lipgloss.Color(p.Accent),

		fgBase: lipgloss.Color(p.Ink),
		bgBase: lipgloss.Color(p.Neutral),

		separator: blend(p.Neutral, p.Ink, 0.3),

		fgSubtle:     blend(p.Ink, p.Neutral, 0.3),
		fgMoreSubtle: blend(p.Ink, p.Neutral, 0.5),
		fgMostSubtle: blend(p.Ink, p.Neutral, 0.7),

		onPrimary: contrastColor(p.Primary, p.Ink, p.Neutral),

		bgMostVisible:  blend(p.Neutral, p.Ink, 0.3),
		bgLessVisible:  blend(p.Neutral, p.Ink, 0.15),
		bgLeastVisible: blend(p.Neutral, p.Ink, 0.06),

		destructive: lipgloss.Color(p.Error),
		error:       lipgloss.Color(p.Error),
		denied:      blend(p.Error, p.Neutral, 0.3),

		warning:       lipgloss.Color(p.Warning),
		warningSubtle: blend(p.Warning, p.Neutral, 0.3),
		busy:          lipgloss.Color(p.Warning),

		info:          lipgloss.Color(p.Info),
		infoMoreSubtle:  blend(p.Info, p.Neutral, 0.4),
		infoMostSubtle:  blend(p.Info, p.Neutral, 0.6),

		success:           lipgloss.Color(p.Success),
		successMoreSubtle: blend(p.Success, p.Neutral, 0.3),
		successMostSubtle: blend(p.Success, p.Neutral, 0.5),
	}
	return quickStyle(opts), nil
}

// blend returns a color that is factor of c2 blended into c1.
// factor 0 → pure c1, factor 1 → pure c2.
func blend(c1, c2 string, factor float64) color.Color {
	r1, g1, b1 := parseHex(c1)
	r2, g2, b2 := parseHex(c2)
	r := uint8(float64(r1)*(1-factor) + float64(r2)*factor)
	g := uint8(float64(g1)*(1-factor) + float64(g2)*factor)
	b := uint8(float64(b1)*(1-factor) + float64(b2)*factor)
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
}

// contrastColor picks the color that contrasts better against the
// reference: if reference is dark, use light; if light, use dark.
func contrastColor(ref, light, dark string) color.Color {
	_, _, l := rgbToHsl(ref)
	if l < 0.5 {
		return lipgloss.Color(light)
	}
	return lipgloss.Color(dark)
}

func parseHex(hex string) (r, g, b uint8) {
	hex = trimHash(hex)
	fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return
}

func trimHash(s string) string {
	if len(s) > 0 && s[0] == '#' {
		return s[1:]
	}
	return s
}

// rgbToHsl converts RGB to HSL (h, s, l in [0,1]). Only l is used here.
func rgbToHsl(hex string) (h, s, l float64) {
	r, g, b := parseHex(hex)
	rf := float64(r) / 255
	gf := float64(g) / 255
	bf := float64(b) / 255
	max := max(rf, gf, bf)
	min := min(rf, gf, bf)
	l = (max + min) / 2
	if max == min {
		return 0, 0, l
	}
	d := max - min
	if l > 0.5 {
		s = d / (2 - max - min)
	} else {
		s = d / (max + min)
	}
	switch max {
	case rf:
		h = (gf - bf) / d
		if gf < bf {
			h += 6
		}
	case gf:
		h = (bf-rf)/d + 2
	case bf:
		h = (rf-gf)/d + 4
	}
	h /= 6
	return
}

// lipglossColor satisfies the color.Color interface check at compile time.
var _ color.Color = lipgloss.Color("")
