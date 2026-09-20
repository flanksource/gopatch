package model

type User struct {
	Name string
}

type Saver interface {
	Save() error
}

type File struct{}

func (File) Save() error { return nil }
