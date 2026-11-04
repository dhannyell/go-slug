module github.com/dhannyell/go-slug/samples

go 1.26.8

require (
	github.com/dhannyell/go-slug v1.0.0
	github.com/dhannyell/webgpu v1.35.2
	github.com/go-gl/glfw/v3.4/glfw v0.1.0-pre.1.0.20260628091122-0bd588dc30cf
	golang.org/x/image v0.43.0
)

require github.com/go-text/typesetting v0.3.5 // indirect

require (
	github.com/dhannyell/go-slug/text v0.0.0
	github.com/dhannyell/webgpu/libs-android v0.0.0-20260905020343-c4069cac051c // indirect
	github.com/dhannyell/webgpu/libs-darwin v0.0.0-20260905020352-092624e3a385 // indirect
	github.com/dhannyell/webgpu/libs-ios v0.0.0-20260905020358-fb13dc722108 // indirect
	github.com/dhannyell/webgpu/libs-linux v0.0.0-20260905020403-4ea5c475cbbd // indirect
	github.com/dhannyell/webgpu/libs-windows v0.0.0-20260905020407-128027db48f4 // indirect
	golang.org/x/text v0.38.0 // indirect
)

replace github.com/dhannyell/go-slug => ../

replace github.com/dhannyell/go-slug/text => ../text
