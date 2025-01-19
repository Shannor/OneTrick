package user

import "time"

type User struct {
	ID                  string       `json:"id" firestore:"id"`
	MemberID            string       `json:"memberId" firestore:"memberId"`
	PrimaryMembershipID string       `json:"primaryMembershipId" firestore:"primaryMembershipId"`
	UniqueName          string       `json:"uniqueName" firestore:"uniqueName"`
	DisplayName         string       `json:"displayName" firestore:"displayName"`
	Memberships         []Membership `json:"memberships" firestore:"memberships"`
	CreatedAt           time.Time    `json:"createdAt" firestore:"createdAt"`
}

type Membership struct {
	ID          string `json:"id" firestore:"id"`
	Type        int64  `json:"type" firestore:"type"`
	DisplayName string `json:"displayName" firestore:"displayName"`
}
