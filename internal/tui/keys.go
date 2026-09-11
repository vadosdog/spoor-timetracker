// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package tui

// A key is a place on the keyboard, not a letter.
//
// Every shortcut here is a Latin letter, and the person this was written for
// spends half their day typing Cyrillic. Switching layouts to press "t" — and
// switching back — is a tax on the one loop that has to fit in two minutes,
// and it is paid at exactly the moment somebody is deciding whether closing
// the day is worth it.
//
// So a keystroke is read by where it is: "е" is the key "t" is printed on, and
// both mean the same thing. The map is the standard ЙЦУКЕН layout laid over
// QWERTY, both cases, so it holds for every shortcut this program will ever
// grow rather than for the handful it has today.
//
// Case is kept, because case means something: a capital is "new" everywhere in
// this interface and a lowercase letter is "nothing". Mapping "Т" to "n" would
// have made shift meaningless on one layout and not the other.
//
// Typed text is not touched. This runs on the shortcut screens only, and the
// line editor takes whatever alphabet somebody is writing in — a project
// called "модель ЗП" has to be typeable.
var sameKeyAs = map[string]string{
	"й": "q", "ц": "w", "у": "e", "к": "r", "е": "t", "н": "y", "г": "u",
	"ш": "i", "щ": "o", "з": "p", "х": "[", "ъ": "]",
	"ф": "a", "ы": "s", "в": "d", "а": "f", "п": "g", "р": "h", "о": "j",
	"л": "k", "д": "l", "ж": ";", "э": "'",
	"я": "z", "ч": "x", "с": "c", "м": "v", "и": "b", "т": "n", "ь": "m",
	"б": ",", "ю": ".", "ё": "`",

	"Й": "Q", "Ц": "W", "У": "E", "К": "R", "Е": "T", "Н": "Y", "Г": "U",
	"Ш": "I", "Щ": "O", "З": "P", "Х": "[", "Ъ": "]", "Ф": "A", "Ы": "S",
	"В": "D", "А": "F", "П": "G", "Р": "H", "О": "J", "Л": "K", "Д": "L",
	"Ж": ";", "Э": "'", "Я": "Z", "Ч": "X", "С": "C", "М": "V", "И": "B",
	"Т": "N", "Ь": "M", "Б": ",", "Ю": ".", "Ё": "`",
}

// key is what a keystroke means here: the Latin letter on the same physical
// key, or the keystroke itself for everything that is not a letter.
func key(pressed string) string {
	if latin, ok := sameKeyAs[pressed]; ok {
		return latin
	}
	return pressed
}
