package runtime

type projectRecord struct {
	ID      string `json:"id"`
	Root    string `json:"root"`
	Common  string `json:"common"`
	Witness string `json:"witness"`
}
type worktreeRecord struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Path    string `json:"path"`
	Common  string `json:"common"`
	Store   string `json:"store"`
	Witness string `json:"witness"`
}
