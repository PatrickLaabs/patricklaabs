package main

import (
	"reflect"
	"testing"
)

// Every interface string is set in every language, so a new string cannot
// quietly render empty on the pages of one of them.
func TestUITextIsComplete(t *testing.T) {
	for _, l := range languages {
		v := reflect.ValueOf(l.Text)
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).IsZero() {
				t.Errorf("%s: UIText.%s is empty; add it in i18n.go", l.Code, v.Type().Field(i).Name)
			}
		}
	}
}
