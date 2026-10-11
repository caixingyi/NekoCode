package tool

// RegisterAll 把六个核心工具注册进 registry，root 为工作目录。
func RegisterAll(r *Registry, root string) {
	r.Register(readFile{root: root})
	r.Register(writeFile{root: root})
	r.Register(editFile{root: root})
	r.Register(runCommand{root: root})
	r.Register(findFiles{root: root})
	r.Register(searchCode{root: root})
}
