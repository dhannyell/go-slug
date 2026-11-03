module github.com/dhannyell/go-slug/text

go 1.24.0

require (
	github.com/dhannyell/go-slug v0.0.0-20261103161456-d589318e258f
	github.com/go-text/typesetting v0.3.5
	github.com/go-text/typesetting-utils v0.0.0-20260419141703-4ffe8874dabc
	golang.org/x/image v0.23.0
)

require golang.org/x/text v0.21.0 // indirect

replace github.com/dhannyell/go-slug => ../
