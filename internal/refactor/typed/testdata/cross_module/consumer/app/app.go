package app

import "example.com/producer/model"

type Other struct {
	Name string
}

func Read(u model.User, o Other) string {
	_ = model.User{Name: u.Name}
	return o.Name
}

func Store(s model.Saver) error {
	var _ model.Saver = model.File{}
	return s.Save()
}
