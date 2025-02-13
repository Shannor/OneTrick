package session

import "cloud.google.com/go/firestore"

type Service interface {
}
type service struct {
	db *firestore.Client
}

var _ Service = (*service)(nil)

func NewService(db *firestore.Client) Service {
	return &service{
		db: db,
	}
}

func (s service) Create() {

}
