package user

import "testing"

func TestUserPrimaryMembership(t *testing.T) {
	t.Run("matches primary membership type", func(t *testing.T) {
		u := &User{
			PrimaryMembershipID: "4611686018467484863",
			Memberships: []Membership{
				{ID: "111", Type: 1},
				{ID: "4611686018467484863", Type: 3},
			},
		}
		pmID, mType, err := u.PrimaryMembership()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pmID != 4611686018467484863 || mType != 3 {
			t.Fatalf("expected (4611686018467484863, 3), got (%d, %d)", pmID, mType)
		}
	})

	t.Run("falls back to first membership type when primary ID not in list", func(t *testing.T) {
		u := &User{
			PrimaryMembershipID: "4611686018467484863",
			Memberships: []Membership{
				{ID: "111", Type: 2},
			},
		}
		pmID, mType, err := u.PrimaryMembership()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pmID != 4611686018467484863 || mType != 2 {
			t.Fatalf("expected (4611686018467484863, 2), got (%d, %d)", pmID, mType)
		}
	})

	t.Run("errors on nil user or empty or non-numeric primary membership ID", func(t *testing.T) {
		var nilUser *User
		if _, _, err := nilUser.PrimaryMembership(); err == nil {
			t.Fatal("expected error for nil user")
		}
		if _, _, err := (&User{}).PrimaryMembership(); err == nil {
			t.Fatal("expected error for empty primary membership ID")
		}
		if _, _, err := (&User{PrimaryMembershipID: "not-a-number"}).PrimaryMembership(); err == nil {
			t.Fatal("expected error for non-numeric primary membership ID")
		}
	})
}
