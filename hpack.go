package minhq

type hpackEntry struct {
	len uint8
	val uint32
}

// Table contains the raw HPACK Huffman table
var hpackTable = []hpackEntry{
	{13, 0x1ff8},
	{23, 0x7fffd8},
	{28, 0xfffffe2},
	{28, 0xfffffe3},
	{28, 0xfffffe4},
	{28, 0xfffffe5},
	{28, 0xfffffe6},
	{28, 0xfffffe7},
	{28, 0xfffffe8},
	{24, 0xffffea},
	{30, 0x3ffffffc},
	{28, 0xfffffe9},
	{28, 0xfffffea},
	{30, 0x3ffffffd},
	{28, 0xfffffeb},
	{28, 0xfffffec},
	{28, 0xfffffed},
	{28, 0xfffffee},
	{28, 0xfffffef},
	{28, 0xffffff0},
	{28, 0xffffff1},
	{28, 0xffffff2},
	{30, 0x3ffffffe},
	{28, 0xffffff3},
	{28, 0xffffff4},
	{28, 0xffffff5},
	{28, 0xffffff6},
	{28, 0xffffff7},
	{28, 0xffffff8},
	{28, 0xffffff9},
	{28, 0xffffffa},
	{28, 0xffffffb},
	{6, 0x14},     // ' '
	{10, 0x3f8},   // !
	{10, 0x3f9},   // '"'
	{12, 0xffa},   // '#'
	{13, 0x1ff9},  // '$'
	{6, 0x15},     // '%'
	{8, 0xf8},     // '&'
	{11, 0x7fa},   // '''
	{10, 0x3fa},   // '('
	{10, 0x3fb},   // ')'
	{8, 0xf9},     // '*'
	{11, 0x7fb},   // '+'
	{8, 0xfa},     // ','
	{6, 0x16},     // '-'
	{6, 0x17},     // '.'
	{6, 0x18},     // '/'
	{5, 0x0},      // '0'
	{5, 0x1},      // '1'
	{5, 0x2},      // '2'
	{6, 0x19},     // '3'
	{6, 0x1a},     // '4'
	{6, 0x1b},     // '5'
	{6, 0x1c},     // '6'
	{6, 0x1d},     // '7'
	{6, 0x1e},     // '8'
	{6, 0x1f},     // '9'
	{7, 0x5c},     // ':'
	{8, 0xfb},     // ';'
	{15, 0x7ffc},  // '<'
	{6, 0x20},     // '='
	{12, 0xffb},   // '>'
	{10, 0x3fc},   // '?'
	{13, 0x1ffa},  // '@'
	{6, 0x21},     // 'A'
	{7, 0x5d},     // 'B'
	{7, 0x5e},     // 'C'
	{7, 0x5f},     // 'D'
	{7, 0x60},     // 'E'
	{7, 0x61},     // 'F'
	{7, 0x62},     // 'G'
	{7, 0x63},     // 'H'
	{7, 0x64},     // 'I'
	{7, 0x65},     // 'J'
	{7, 0x66},     // 'K'
	{7, 0x67},     // 'L'
	{7, 0x68},     // 'M'
	{7, 0x69},     // 'N'
	{7, 0x6a},     // 'O'
	{7, 0x6b},     // 'P'
	{7, 0x6c},     // 'Q'
	{7, 0x6d},     // 'R'
	{7, 0x6e},     // 'S'
	{7, 0x6f},     // 'T'
	{7, 0x70},     // 'U'
	{7, 0x71},     // 'V'
	{7, 0x72},     // 'W'
	{8, 0xfc},     // 'X'
	{7, 0x73},     // 'Y'
	{8, 0xfd},     // 'Z'
	{13, 0x1ffb},  // '['
	{19, 0x7fff0}, // '\'
	{13, 0x1ffc},  // ']'
	{14, 0x3ffc},  // '^'
	{6, 0x22},     // '_'
	{15, 0x7ffd},  // '`'
	{5, 0x3},      // 'a'
	{6, 0x23},     // 'b'
	{5, 0x4},      // 'c'
	{6, 0x24},     // 'd'
	{5, 0x5},      // 'e'
	{6, 0x25},     // 'f'
	{6, 0x26},     // 'g'
	{6, 0x27},     // 'h'
	{5, 0x6},      // 'i'
	{7, 0x74},     // 'j'
	{7, 0x75},     // 'k'
	{6, 0x28},     // 'l'
	{6, 0x29},     // 'm'
	{6, 0x2a},     // 'n'
	{5, 0x7},      // 'o'
	{6, 0x2b},     // 'p'
	{7, 0x76},     // 'q'
	{6, 0x2c},     // 'r'
	{5, 0x8},      // 's'
	{5, 0x9},      // 't'
	{6, 0x2d},     // 'u'
	{7, 0x77},     // 'v'
	{7, 0x78},     // 'w'
	{7, 0x79},     // 'x'
	{7, 0x7a},     // 'y'
	{7, 0x7b},     // 'z'
	{15, 0x7ffe},  // '{'
	{11, 0x7fc},   // '|'
	{14, 0x3ffd},  // '}'
	{13, 0x1ffd},  // ~
	{28, 0xffffffc},
	{20, 0xfffe6},
	{22, 0x3fffd2},
	{20, 0xfffe7},
	{20, 0xfffe8},
	{22, 0x3fffd3},
	{22, 0x3fffd4},
	{22, 0x3fffd5},
	{23, 0x7fffd9},
	{22, 0x3fffd6},
	{23, 0x7fffda},
	{23, 0x7fffdb},
	{23, 0x7fffdc},
	{23, 0x7fffdd},
	{23, 0x7fffde},
	{24, 0xffffeb},
	{23, 0x7fffdf},
	{24, 0xffffec},
	{24, 0xffffed},
	{22, 0x3fffd7},
	{23, 0x7fffe0},
	{24, 0xffffee},
	{23, 0x7fffe1},
	{23, 0x7fffe2},
	{23, 0x7fffe3},
	{23, 0x7fffe4},
	{21, 0x1fffdc},
	{22, 0x3fffd8},
	{23, 0x7fffe5},
	{22, 0x3fffd9},
	{23, 0x7fffe6},
	{23, 0x7fffe7},
	{24, 0xffffef},
	{22, 0x3fffda},
	{21, 0x1fffdd},
	{20, 0xfffe9},
	{22, 0x3fffdb},
	{22, 0x3fffdc},
	{23, 0x7fffe8},
	{23, 0x7fffe9},
	{21, 0x1fffde},
	{23, 0x7fffea},
	{22, 0x3fffdd},
	{22, 0x3fffde},
	{24, 0xfffff0},
	{21, 0x1fffdf},
	{22, 0x3fffdf},
	{23, 0x7fffeb},
	{23, 0x7fffec},
	{21, 0x1fffe0},
	{21, 0x1fffe1},
	{22, 0x3fffe0},
	{21, 0x1fffe2},
	{23, 0x7fffed},
	{22, 0x3fffe1},
	{23, 0x7fffee},
	{23, 0x7fffef},
	{20, 0xfffea},
	{22, 0x3fffe2},
	{22, 0x3fffe3},
	{22, 0x3fffe4},
	{23, 0x7ffff0},
	{22, 0x3fffe5},
	{22, 0x3fffe6},
	{23, 0x7ffff1},
	{26, 0x3ffffe0},
	{26, 0x3ffffe1},
	{20, 0xfffeb},
	{19, 0x7fff1},
	{22, 0x3fffe7},
	{23, 0x7ffff2},
	{22, 0x3fffe8},
	{25, 0x1ffffec},
	{26, 0x3ffffe2},
	{26, 0x3ffffe3},
	{26, 0x3ffffe4},
	{27, 0x7ffffde},
	{27, 0x7ffffdf},
	{26, 0x3ffffe5},
	{24, 0xfffff1},
	{25, 0x1ffffed},
	{19, 0x7fff2},
	{21, 0x1fffe3},
	{26, 0x3ffffe6},
	{27, 0x7ffffe0},
	{27, 0x7ffffe1},
	{26, 0x3ffffe7},
	{27, 0x7ffffe2},
	{24, 0xfffff2},
	{21, 0x1fffe4},
	{21, 0x1fffe5},
	{26, 0x3ffffe8},
	{26, 0x3ffffe9},
	{28, 0xffffffd},
	{27, 0x7ffffe3},
	{27, 0x7ffffe4},
	{27, 0x7ffffe5},
	{20, 0xfffec},
	{24, 0xfffff3},
	{20, 0xfffed},
	{21, 0x1fffe6},
	{22, 0x3fffe9},
	{21, 0x1fffe7},
	{21, 0x1fffe8},
	{23, 0x7ffff3},
	{22, 0x3fffea},
	{22, 0x3fffeb},
	{25, 0x1ffffee},
	{25, 0x1ffffef},
	{24, 0xfffff4},
	{24, 0xfffff5},
	{26, 0x3ffffea},
	{23, 0x7ffff4},
	{26, 0x3ffffeb},
	{27, 0x7ffffe6},
	{26, 0x3ffffec},
	{26, 0x3ffffed},
	{27, 0x7ffffe7},
	{27, 0x7ffffe8},
	{27, 0x7ffffe9},
	{27, 0x7ffffea},
	{27, 0x7ffffeb},
	{28, 0xffffffe},
	{27, 0x7ffffec},
	{27, 0x7ffffed},
	{27, 0x7ffffee},
	{27, 0x7ffffef},
	{27, 0x7fffff0},
	{26, 0x3ffffee},
	//	{30, 0x3fffffff},
}
