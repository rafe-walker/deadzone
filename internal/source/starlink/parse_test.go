package starlink

import "testing"

func TestToFloatRejectsProtobufNonFiniteStrings(t *testing.T) {
	for _, in := range []string{"NaN", "Infinity", "-Infinity", "nan"} {
		if got := toFloat(in); got != nil {
			t.Errorf("toFloat(%q) = %v, want nil", in, *got)
		}
	}
	if got := toFloat("12.5"); got == nil || *got != 12.5 {
		t.Errorf("toFloat(\"12.5\") = %v", got)
	}
}
