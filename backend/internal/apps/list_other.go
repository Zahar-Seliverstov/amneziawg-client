//go:build !linux && !windows

package apps

// List вне Linux: правила для программ здесь не действуют, и предлагать
// выбор незачем.
func List() Catalog {
	return Catalog{Supported: false, Apps: []App{}}
}
