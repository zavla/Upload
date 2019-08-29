package main

import (
	"strconv"
	"testing"
)

func TestStrconv(t *testing.T) {
	var i int64
	i = 123456789
	t.Run("123456789", func(t *testing.T) {
		s1 := strconv.FormatInt(i, 10)
		s2 := string(i)
		if s1 == s2 {
			t.Errorf("%s , %s", s1, s2)
		}
	})
}

// func TestSliceToString(t *testing.T) {
// 	h := sha1.New()
// 	h.Write([]byte("firefox"))
// 	sl := h.Sum(nil)
// 	t.Errorf("%#x\n", sl)
// 	t.Errorf("% x\n", sl)
// 	t.Errorf("%x\n", sl)
// 	t.Errorf("%s\n", sl)
// 	t.Errorf("%v\n", sl)
// 	t.Errorf("%#v\n", sl)

// }
