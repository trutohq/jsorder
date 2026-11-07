package jsorder

import "testing"

// The expected strings are JSON.stringify's, captured with Bun 1.3:
//
//	bun -e 'const v={a:1,b:[1,{c:"x\u2028y"},[]],d:{},e:{"2":true,"1":null}};
//	        console.log(JSON.stringify(JSON.stringify(v,null,2)))'
func TestMarshalJSWithIndentIsJSONStringifys(t *testing.T) {
	v := Pairs("a", 1.0,
		"b", []any{1.0, Pairs("c", "x\u2028y"), []any{}},
		"d", New(),
		"e", Pairs("2", true, "1", Null))
	cases := []struct {
		indent string
		v      any
		want   string
	}{
		{"  ", v, "{\n  \"a\": 1,\n  \"b\": [\n    1,\n    {\n      \"c\": \"x\u2028y\"\n    },\n    []\n  ],\n  \"d\": {},\n  \"e\": {\n    \"1\": null,\n    \"2\": true\n  }\n}"},
		{"  ", []any{}, "[]"},
		{"  ", New(), "{}"},
		{"abcdefghijklmno", Pairs("a", 1.0, "b", 2.0), "{\nabcdefghij\"a\": 1,\nabcdefghij\"b\": 2\n}"},
		{"", v, `{"a":1,"b":[1,{"c":"x` + "\u2028" + `y"},[]],"d":{},"e":{"1":null,"2":true}}`},
	}
	for _, c := range cases {
		got, err := MarshalJSWith(c.v, Options{Indent: c.indent})
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want {
			t.Errorf("indent %q:\n got %q\nwant %q", c.indent, got, c.want)
		}
	}
}

type hostThing struct{ json any }

// The replacer stands in for toJSON: it maps a value the encoder does not know
// to the JSON value model. A nil it returns omits an object member, is null in
// an array, and "null" at the top level, as an undefined toJSON result is.
//
//	bun -e 'const h={toJSON(){return undefined}};
//	        console.log(JSON.stringify({a:h,b:[h],c:{toJSON(){return {x:1}}}}), JSON.stringify(h))'
//	{"b":[null],"c":{"x":1}} undefined
func TestMarshalJSWithReplacerIsToJSON(t *testing.T) {
	replace := func(v any) any {
		if h, ok := v.(hostThing); ok {
			return h.json
		}
		return v
	}
	v := Pairs("a", hostThing{nil}, "b", []any{hostThing{nil}}, "c", hostThing{Pairs("x", 1.0)})
	got, err := MarshalJSWith(v, Options{Replacer: replace})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"b":[null],"c":{"x":1}}` {
		t.Errorf("got %s", got)
	}
	top, err := MarshalJSWith(hostThing{nil}, Options{Replacer: replace})
	if err != nil || string(top) != "null" {
		t.Errorf("top-level undefined = %q, %v", top, err)
	}
	if _, err := MarshalJS(hostThing{nil}); err == nil {
		t.Error("MarshalJS of an unknown type must still refuse without a replacer")
	}
}
