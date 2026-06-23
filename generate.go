package unpixel

// Regenerate the recovery-test reference images and their manifest:
//
//	go generate ./...
//
//go:generate go run ./internal/fixture/gen -out testdata/fixtures
//go:generate go run ./internal/fixture/gensick -out testdata/sick
