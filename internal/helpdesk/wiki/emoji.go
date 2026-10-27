package wiki

import "unicode"

// isEmoji reports whether s is one emoji as Unicode spells them (UTS #51): a pictograph
// with an optional variation selector and skin tone, pictographs joined by ZWJ (families,
// professions), a flag (two regional indicators, or a black flag with tag characters), or
// a keycap. Anything else, such as letters, bidi controls, zero-width characters other
// than the joiner, or combining marks, is not.
func isEmoji(s string) bool {
	rs := []rune(s)
	if len(rs) == 0 || len(rs) > 16 {
		return false
	}
	switch r := rs[0]; {
	case r >= '0' && r <= '9' || r == '#' || r == '*':
		rest := rs[1:]
		if len(rest) > 0 && rest[0] == variation {
			rest = rest[1:]
		}
		return len(rest) == 1 && rest[0] == keycap
	case isRegional(r):
		return len(rs) == 2 && isRegional(rs[1])
	}
	for i := 0; ; {
		n := element(rs[i:])
		if n == 0 {
			return false
		}
		i += n
		if i == len(rs) {
			return true
		}
		if rs[i] != zwj || i+1 == len(rs) {
			return false
		}
		i++
	}
}

const (
	zwj       = 0x200d
	variation = 0xfe0f
	keycap    = 0x20e3
	blackFlag = 0x1f3f4
	cancelTag = 0xe007f
)

// element is how many runes of rs make one pictograph with what may follow it: a
// variation selector, a skin tone, or (after a black flag) tags up to the cancel tag.
// 0: rs does not start with one.
func element(rs []rune) int {
	r := rs[0]
	if !unicode.Is(pictographic, r) && !isSkinTone(r) {
		return 0
	}
	i := 1
	if r == blackFlag && i < len(rs) && isTag(rs[i]) {
		for i < len(rs) && isTag(rs[i]) {
			i++
		}
		if i == len(rs) || rs[i] != cancelTag {
			return 0
		}
		return i + 1
	}
	if i < len(rs) && (rs[i] == variation || rs[i] == 0xfe0e) {
		i++
	}
	if i < len(rs) && isSkinTone(rs[i]) && !isSkinTone(r) {
		i++
	}
	return i
}

func isRegional(r rune) bool { return r >= 0x1f1e6 && r <= 0x1f1ff }
func isSkinTone(r rune) bool { return r >= 0x1f3fb && r <= 0x1f3ff }
func isTag(r rune) bool      { return r >= 0xe0020 && r <= 0xe007e }

// pictographic is Extended_Pictographic from Unicode's emoji-data.txt (15.0), which
// also reserves the blocks future emoji are added to.
var pictographic = &unicode.RangeTable{
	R16: []unicode.Range16{
		{0x00a9, 0x00a9, 1},
		{0x00ae, 0x00ae, 1},
		{0x203c, 0x203c, 1},
		{0x2049, 0x2049, 1},
		{0x2122, 0x2122, 1},
		{0x2139, 0x2139, 1},
		{0x2194, 0x2199, 1},
		{0x21a9, 0x21aa, 1},
		{0x231a, 0x231b, 1},
		{0x2328, 0x2328, 1},
		{0x2388, 0x2388, 1},
		{0x23cf, 0x23cf, 1},
		{0x23e9, 0x23f3, 1},
		{0x23f8, 0x23fa, 1},
		{0x24c2, 0x24c2, 1},
		{0x25aa, 0x25ab, 1},
		{0x25b6, 0x25b6, 1},
		{0x25c0, 0x25c0, 1},
		{0x25fb, 0x25fe, 1},
		{0x2600, 0x2605, 1},
		{0x2607, 0x2612, 1},
		{0x2614, 0x2685, 1},
		{0x2690, 0x2705, 1},
		{0x2708, 0x2712, 1},
		{0x2714, 0x2714, 1},
		{0x2716, 0x2716, 1},
		{0x271d, 0x271d, 1},
		{0x2721, 0x2721, 1},
		{0x2728, 0x2728, 1},
		{0x2733, 0x2734, 1},
		{0x2744, 0x2744, 1},
		{0x2747, 0x2747, 1},
		{0x274c, 0x274c, 1},
		{0x274e, 0x274e, 1},
		{0x2753, 0x2755, 1},
		{0x2757, 0x2757, 1},
		{0x2763, 0x2767, 1},
		{0x2795, 0x2797, 1},
		{0x27a1, 0x27a1, 1},
		{0x27b0, 0x27b0, 1},
		{0x27bf, 0x27bf, 1},
		{0x2934, 0x2935, 1},
		{0x2b05, 0x2b07, 1},
		{0x2b1b, 0x2b1c, 1},
		{0x2b50, 0x2b50, 1},
		{0x2b55, 0x2b55, 1},
		{0x3030, 0x3030, 1},
		{0x303d, 0x303d, 1},
		{0x3297, 0x3297, 1},
		{0x3299, 0x3299, 1},
	},
	R32: []unicode.Range32{
		{0x1f000, 0x1f0ff, 1},
		{0x1f10d, 0x1f10f, 1},
		{0x1f12f, 0x1f12f, 1},
		{0x1f16c, 0x1f171, 1},
		{0x1f17e, 0x1f17f, 1},
		{0x1f18e, 0x1f18e, 1},
		{0x1f191, 0x1f19a, 1},
		{0x1f1ad, 0x1f1e5, 1},
		{0x1f201, 0x1f20f, 1},
		{0x1f21a, 0x1f21a, 1},
		{0x1f22f, 0x1f22f, 1},
		{0x1f232, 0x1f23a, 1},
		{0x1f23c, 0x1f23f, 1},
		{0x1f249, 0x1f3fa, 1},
		{0x1f400, 0x1f53d, 1},
		{0x1f546, 0x1f64f, 1},
		{0x1f680, 0x1f6ff, 1},
		{0x1f774, 0x1f77f, 1},
		{0x1f7d5, 0x1f7ff, 1},
		{0x1f80c, 0x1f80f, 1},
		{0x1f848, 0x1f84f, 1},
		{0x1f85a, 0x1f85f, 1},
		{0x1f888, 0x1f88f, 1},
		{0x1f8ae, 0x1f8ff, 1},
		{0x1f90c, 0x1f93a, 1},
		{0x1f93c, 0x1f945, 1},
		{0x1f947, 0x1faff, 1},
		{0x1fc00, 0x1fffd, 1},
	},
	LatinOffset: 2,
}
