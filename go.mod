module github.com/trutohq/jsorder

go 1.27.0

// jsbuiltin is not published; consumers carry the same replace until it is.
replace github.com/trutohq/jsbuiltin => ../jsbuiltin

require (
	github.com/recolabs/gnata v0.6.2
	github.com/trutohq/jsbuiltin v0.0.0
)

require (
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	golang.org/x/text v0.21.0 // indirect
)
