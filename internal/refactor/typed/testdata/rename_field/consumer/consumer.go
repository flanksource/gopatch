package consumer

import "example.com/renamefield/user"

func Describe(u *user.User) string {
	return u.Name
}

func Build() *user.User {
	return &user.User{Name: "ann", Age: 3}
}

type local struct {
	Name string
}

func (l local) get() string { return l.Name }
