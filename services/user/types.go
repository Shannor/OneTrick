package user

type User struct {
	ID                  string       `json:"id" firestore:"id"`
	PrimaryMembershipID string       `json:"primaryMembershipId" firestore:"primaryMembershipId"`
	UniqueName          string       `json:"uniqueName" firestore:"uniqueName"`
	DisplayName         string       `json:"displayName" firestore:"displayName"`
	Memberships         []Membership `json:"memberships" firestore:"memberships"`
}

type Membership struct {
	ID          string `json:"id" firestore:"id"`
	Type        int64  `json:"type" firestore:"type"`
	DisplayName string `json:"displayName" firestore:"displayName"`
}
