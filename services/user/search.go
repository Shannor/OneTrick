package user

import (
	"context"
	"errors"
	"oneTrick/api"
	"oneTrick/utils"
	"slices"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
	"google.golang.org/api/iterator"
)

const (
	firestoreInBatchLimit      = 30
	firestoreOrInBatchLimit    = 15
	firestorePrefixLimit       = 25
	legacyUsersCacheTTL        = 5 * time.Minute
	defaultBungieSearchTimeout = 4 * time.Second
	firestorePrefixHighEnd     = "\uf8ff"
)

type userStore interface {
	findUserByLookupID(ctx context.Context, id string) (*User, error)
	findUsersByPrefix(ctx context.Context, queryLower string, rawTrimmed string) ([]User, error)
	findUsersByMembershipIDs(ctx context.Context, bungieNetIDs []string, primaryMembershipIDs []string) ([]User, error)
	findUsersByPartyMembershipIDs(ctx context.Context, membershipIDs []string) ([]User, error)
}

type firestoreUserStore struct {
	db *firestore.Client

	// loadAllUsersFn defaults to scanning Firestore's users collection and can be overridden in unit tests.
	loadAllUsersFn func(ctx context.Context) ([]User, error)

	refreshMu        sync.Mutex
	legacyMu         sync.RWMutex
	legacyUsers      []User
	legacyExpiresAt  time.Time
	legacyAllIndexed bool
}

func newFirestoreUserStore(db *firestore.Client) *firestoreUserStore {
	return &firestoreUserStore{db: db}
}

func (f *firestoreUserStore) findUserByLookupID(ctx context.Context, id string) (*User, error) {
	if f.db == nil {
		return nil, NotFound
	}
	orFilter := firestore.OrFilter{
		Filters: []firestore.EntityFilter{
			firestore.PropertyFilter{Path: "id", Operator: "==", Value: id},
			firestore.PropertyFilter{Path: "memberId", Operator: "==", Value: id},
			firestore.PropertyFilter{Path: "primaryMembershipId", Operator: "==", Value: id},
		},
	}

	iter := f.db.Collection(userCollection).WhereEntity(orFilter).Documents(ctx)
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		var u User
		if err := doc.DataTo(&u); err != nil {
			return nil, err
		}
		return &u, nil
	}
	return nil, NotFound
}

func (f *firestoreUserStore) evictLegacyUser(userID string) {
	if userID == "" {
		return
	}
	f.legacyMu.Lock()
	defer f.legacyMu.Unlock()
	if len(f.legacyUsers) == 0 {
		return
	}
	filtered := make([]User, 0, len(f.legacyUsers))
	for _, u := range f.legacyUsers {
		if u.ID != userID {
			filtered = append(filtered, u)
		}
	}
	f.legacyUsers = filtered
	if !f.legacyExpiresAt.IsZero() && len(f.legacyUsers) == 0 {
		f.legacyAllIndexed = true
	}
}

func (f *firestoreUserStore) findUsersByPrefix(ctx context.Context, queryLower string, rawTrimmed string) ([]User, error) {
	if f.db == nil || queryLower == "" {
		return nil, nil
	}

	byID := make(map[string]User)
	addUsers := func(users []User) {
		for _, u := range users {
			if u.ID != "" {
				byID[u.ID] = u
			}
		}
	}

	endBound := queryLower + firestorePrefixHighEnd

	displayDocs, err := f.db.Collection(userCollection).
		Where("displayNameLower", ">=", queryLower).
		Where("displayNameLower", "<=", endBound).
		Limit(firestorePrefixLimit).
		Documents(ctx).
		GetAll()
	if err != nil {
		return nil, err
	}
	displayUsers, err := utils.GetAllToStructs[User](displayDocs)
	if err != nil {
		return nil, err
	}
	addUsers(displayUsers)

	uniqueDocs, err := f.db.Collection(userCollection).
		Where("uniqueNameLower", ">=", queryLower).
		Where("uniqueNameLower", "<=", endBound).
		Limit(firestorePrefixLimit).
		Documents(ctx).
		GetAll()
	if err != nil {
		return nil, err
	}
	uniqueUsers, err := utils.GetAllToStructs[User](uniqueDocs)
	if err != nil {
		return nil, err
	}
	addUsers(uniqueUsers)

	if isASCIIDigits(rawTrimmed) {
		orFilter := firestore.OrFilter{
			Filters: []firestore.EntityFilter{
				firestore.PropertyFilter{Path: "memberId", Operator: "==", Value: rawTrimmed},
				firestore.PropertyFilter{Path: "primaryMembershipId", Operator: "==", Value: rawTrimmed},
			},
		}
		idDocs, err := f.db.Collection(userCollection).
			WhereEntity(orFilter).
			Limit(firestorePrefixLimit).
			Documents(ctx).
			GetAll()
		if err != nil {
			return nil, err
		}
		idUsers, err := utils.GetAllToStructs[User](idDocs)
		if err != nil {
			return nil, err
		}
		addUsers(idUsers)
	}

	// Fallback for existing Firestore user documents created before displayNameLower / uniqueNameLower existed.
	legacyUsers, err := f.getLegacyUnindexedUsers(ctx)
	if err == nil && len(legacyUsers) > 0 {
		for _, u := range matchLegacyUsers(legacyUsers, queryLower, byID, firestorePrefixLimit) {
			byID[u.ID] = u
		}
	}

	results := make([]User, 0, len(byID))
	for _, u := range byID {
		results = append(results, u)
	}
	return results, nil
}

func matchLegacyUsers(legacyUsers []User, queryLower string, existing map[string]User, limit int) []User {
	if limit <= 0 || queryLower == "" {
		return nil
	}
	matched := make([]User, 0)
	for _, u := range legacyUsers {
		if len(matched) >= limit {
			break
		}
		if _, exists := existing[u.ID]; exists {
			continue
		}
		dLower := strings.ToLower(strings.TrimSpace(u.DisplayName))
		uLower := strings.ToLower(strings.TrimSpace(u.UniqueName))
		if strings.HasPrefix(dLower, queryLower) || strings.HasPrefix(uLower, queryLower) {
			matched = append(matched, u)
		}
	}
	return matched
}

func (f *firestoreUserStore) getLegacyUnindexedUsers(ctx context.Context) ([]User, error) {
	now := time.Now()
	f.legacyMu.RLock()
	if f.legacyAllIndexed {
		f.legacyMu.RUnlock()
		return nil, nil
	}
	if !f.legacyExpiresAt.IsZero() && now.Before(f.legacyExpiresAt) {
		cached := f.legacyUsers
		f.legacyMu.RUnlock()
		return cached, nil
	}
	f.legacyMu.RUnlock()

	f.refreshMu.Lock()
	defer f.refreshMu.Unlock()

	now = time.Now()
	f.legacyMu.RLock()
	if f.legacyAllIndexed {
		f.legacyMu.RUnlock()
		return nil, nil
	}
	if !f.legacyExpiresAt.IsZero() && now.Before(f.legacyExpiresAt) {
		cached := f.legacyUsers
		f.legacyMu.RUnlock()
		return cached, nil
	}
	f.legacyMu.RUnlock()

	allUsers, err := f.loadAllUsers(ctx)
	if err != nil {
		return nil, err
	}

	legacy := make([]User, 0)
	for _, u := range allUsers {
		if (u.DisplayName != "" && u.DisplayNameLower == "") || (u.UniqueName != "" && u.UniqueNameLower == "") {
			legacy = append(legacy, u)
		}
	}

	f.legacyMu.Lock()
	f.legacyUsers = legacy
	f.legacyExpiresAt = now.Add(legacyUsersCacheTTL)
	if len(legacy) == 0 {
		f.legacyAllIndexed = true
	}
	f.legacyMu.Unlock()

	return legacy, nil
}

func (f *firestoreUserStore) loadAllUsers(ctx context.Context) ([]User, error) {
	if f.loadAllUsersFn != nil {
		return f.loadAllUsersFn(ctx)
	}
	if f.db == nil {
		return nil, nil
	}
	docs, err := f.db.Collection(userCollection).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	return utils.GetAllToStructs[User](docs)
}

func (f *firestoreUserStore) findUsersByMembershipIDs(ctx context.Context, bungieNetIDs []string, primaryMembershipIDs []string) ([]User, error) {
	if f.db == nil {
		return nil, nil
	}

	byID := make(map[string]User)
	addUsers := func(users []User) {
		for _, u := range users {
			if u.ID != "" {
				byID[u.ID] = u
			}
		}
	}

	for _, chunk := range chunkUniqueStrings(bungieNetIDs, firestoreInBatchLimit) {
		docs, err := f.db.Collection(userCollection).
			Where("memberId", "in", chunk).
			Documents(ctx).
			GetAll()
		if err != nil {
			return nil, err
		}
		users, err := utils.GetAllToStructs[User](docs)
		if err != nil {
			return nil, err
		}
		addUsers(users)
	}

	for _, chunk := range chunkUniqueStrings(primaryMembershipIDs, firestoreInBatchLimit) {
		docs, err := f.db.Collection(userCollection).
			Where("primaryMembershipId", "in", chunk).
			Documents(ctx).
			GetAll()
		if err != nil {
			return nil, err
		}
		users, err := utils.GetAllToStructs[User](docs)
		if err != nil {
			return nil, err
		}
		addUsers(users)
	}

	results := make([]User, 0, len(byID))
	for _, u := range byID {
		results = append(results, u)
	}
	return results, nil
}

func (f *firestoreUserStore) findUsersByPartyMembershipIDs(ctx context.Context, membershipIDs []string) ([]User, error) {
	if f.db == nil {
		return nil, nil
	}
	chunks := chunkUniqueStrings(membershipIDs, firestoreOrInBatchLimit)
	if len(chunks) == 0 {
		return nil, nil
	}

	byID := make(map[string]User, len(membershipIDs))
	for _, chunk := range chunks {
		orFilter := firestore.OrFilter{
			Filters: []firestore.EntityFilter{
				firestore.PropertyFilter{Path: "primaryMembershipId", Operator: "in", Value: chunk},
				firestore.PropertyFilter{Path: "memberId", Operator: "in", Value: chunk},
			},
		}
		docs, err := f.db.Collection(userCollection).
			WhereEntity(orFilter).
			Documents(ctx).
			GetAll()
		if err != nil {
			return nil, err
		}
		users, err := utils.GetAllToStructs[User](docs)
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			if u.ID != "" {
				byID[u.ID] = u
			}
		}
	}

	results := make([]User, 0, len(byID))
	for _, u := range byID {
		results = append(results, u)
	}
	return results, nil
}

func (s *userService) bungieSearchTimeout() time.Duration {
	if s.bungieTimeout > 0 {
		return s.bungieTimeout
	}
	return defaultBungieSearchTimeout
}

func (s *userService) Search(ctx context.Context, query string, page int) ([]api.SearchUserResult, bool, error) {
	trimmed := strings.TrimSpace(query)
	qLower := strings.ToLower(trimmed)

	clog.WithGroupFields(ctx, "userService.search", clog.Fields{
		"page":     page,
		"queryLen": len(trimmed),
	})

	if trimmed == "" || page < 0 {
		clog.WithGroupFields(ctx, "userService.search", clog.Fields{
			"firestoreMatches": 0,
			"bungieMatches":    0,
			"recordedCount":    0,
			"returnedCount":    0,
			"hasMore":          false,
		})
		return []api.SearchUserResult{}, false, nil
	}

	store := s.resolveStore()

	var (
		firestoreUsers []User
		firestoreErr   error
		wg             sync.WaitGroup
	)
	if page == 0 && store != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			firestoreUsers, firestoreErr = store.findUsersByPrefix(ctx, qLower, trimmed)
		}()
	}

	var (
		bungieResults []api.SearchUserResult
		hasMore       bool
		bungieErr     error
	)
	if s.d2Service != nil {
		bungieCtx, cancel := context.WithTimeout(ctx, s.bungieSearchTimeout())
		bungieResults, hasMore, bungieErr = s.d2Service.Search(bungieCtx, trimmed, int32(page))
		cancel()
	}

	wg.Wait()

	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	if firestoreErr != nil {
		return nil, false, firestoreErr
	}
	if bungieErr != nil {
		clog.WithGroupFields(ctx, "userService.search", clog.Fields{
			"bungieDegraded": true,
			"warning":        bungieErr.Error(),
		})
		bungieResults = nil
		hasMore = false
	}

	crossRefUsers := make([]User, 0)
	if len(bungieResults) > 0 && store != nil {
		bungieNetIDs := make([]string, 0, len(bungieResults))
		primaryIDs := make([]string, 0, len(bungieResults))
		for _, r := range bungieResults {
			if r.BungieNetMembershipID != nil && *r.BungieNetMembershipID != "" {
				bungieNetIDs = append(bungieNetIDs, *r.BungieNetMembershipID)
			}
			if r.PrimaryMembershipID != "" {
				primaryIDs = append(primaryIDs, r.PrimaryMembershipID)
			}
		}
		if len(bungieNetIDs) > 0 || len(primaryIDs) > 0 {
			var err error
			crossRefUsers, err = store.findUsersByMembershipIDs(ctx, bungieNetIDs, primaryIDs)
			if err != nil {
				return nil, false, err
			}
		}
	}

	recordedByPrimary := make(map[string]User, len(firestoreUsers)+len(crossRefUsers))
	recordedByMember := make(map[string]User, len(firestoreUsers)+len(crossRefUsers))
	indexUser := func(u User) {
		if u.PrimaryMembershipID != "" {
			recordedByPrimary[u.PrimaryMembershipID] = u
		}
		if u.MemberID != "" {
			recordedByMember[u.MemberID] = u
		}
	}
	for _, u := range firestoreUsers {
		indexUser(u)
	}
	for _, u := range crossRefUsers {
		indexUser(u)
	}

	deduper := newSearchResultDeduper()

	for idx, hit := range bungieResults {
		matchedUser, matched := matchRecordedUser(hit, recordedByPrimary, recordedByMember)
		if matched {
			hit = mergeBungieWithUser(hit, matchedUser)
		}
		deduper.addOrMerge(hit, idx)
	}

	for _, u := range firestoreUsers {
		deduper.addOrMerge(userToSearchResult(u), -1)
	}

	ranked := deduper.items
	slices.SortStableFunc(ranked, func(a, b rankedSearchResult) int {
		ra := rankTier(a.result, trimmed)
		rb := rankTier(b.result, trimmed)
		if ra != rb {
			return ra - rb
		}
		if !a.result.IsRecorded && !b.result.IsRecorded {
			if a.bungieIndex != b.bungieIndex {
				return a.bungieIndex - b.bungieIndex
			}
		}
		if c := strings.Compare(strings.ToLower(a.result.DisplayName), strings.ToLower(b.result.DisplayName)); c != 0 {
			return c
		}
		if c := strings.Compare(strings.ToLower(a.result.UniqueName), strings.ToLower(b.result.UniqueName)); c != 0 {
			return c
		}
		return strings.Compare(a.result.PrimaryMembershipID, b.result.PrimaryMembershipID)
	})

	results := make([]api.SearchUserResult, 0, len(ranked))
	recordedCount := 0
	for _, item := range ranked {
		if item.result.IsRecorded {
			recordedCount++
		}
		results = append(results, item.result)
	}

	clog.WithGroupFields(ctx, "userService.search", clog.Fields{
		"firestoreMatches": len(firestoreUsers),
		"bungieMatches":    len(bungieResults),
		"recordedCount":    recordedCount,
		"returnedCount":    len(results),
		"hasMore":          hasMore,
	})
	return results, hasMore, nil
}

func (s *userService) resolveStore() userStore {
	if s.store != nil {
		return s.store
	}
	if s.db != nil {
		return newFirestoreUserStore(s.db)
	}
	return nil
}

type rankedSearchResult struct {
	result      api.SearchUserResult
	bungieIndex int
}

type searchResultDeduper struct {
	items         []rankedSearchResult
	byUserID      map[string]int
	byPrimaryID   map[string]int
	byBungieNetID map[string]int
}

func newSearchResultDeduper() *searchResultDeduper {
	return &searchResultDeduper{
		items:         make([]rankedSearchResult, 0),
		byUserID:      make(map[string]int),
		byPrimaryID:   make(map[string]int),
		byBungieNetID: make(map[string]int),
	}
}

func (d *searchResultDeduper) findIndex(r api.SearchUserResult) (int, bool) {
	if r.UserID != nil && *r.UserID != "" {
		if idx, ok := d.byUserID[*r.UserID]; ok {
			return idx, true
		}
	}
	if r.PrimaryMembershipID != "" {
		if idx, ok := d.byPrimaryID[r.PrimaryMembershipID]; ok {
			return idx, true
		}
	}
	if r.BungieNetMembershipID != nil && *r.BungieNetMembershipID != "" {
		if idx, ok := d.byBungieNetID[*r.BungieNetMembershipID]; ok {
			return idx, true
		}
	}
	return -1, false
}

func (d *searchResultDeduper) registerKeys(r api.SearchUserResult, idx int) {
	if r.UserID != nil && *r.UserID != "" {
		d.byUserID[*r.UserID] = idx
	}
	if r.PrimaryMembershipID != "" {
		d.byPrimaryID[r.PrimaryMembershipID] = idx
	}
	if r.BungieNetMembershipID != nil && *r.BungieNetMembershipID != "" {
		d.byBungieNetID[*r.BungieNetMembershipID] = idx
	}
}

func (d *searchResultDeduper) addOrMerge(r api.SearchUserResult, bungieIndex int) {
	if r.AlternateNames == nil {
		r.AlternateNames = []string{}
	}
	if existingIdx, found := d.findIndex(r); found {
		merged := mergeSearchResults(d.items[existingIdx].result, r)
		d.items[existingIdx].result = merged
		if d.items[existingIdx].bungieIndex < 0 && bungieIndex >= 0 {
			d.items[existingIdx].bungieIndex = bungieIndex
		}
		d.registerKeys(merged, existingIdx)
		return
	}

	idx := len(d.items)
	d.items = append(d.items, rankedSearchResult{
		result:      r,
		bungieIndex: bungieIndex,
	})
	d.registerKeys(r, idx)
}

func matchRecordedUser(hit api.SearchUserResult, byPrimary map[string]User, byMember map[string]User) (User, bool) {
	if hit.PrimaryMembershipID != "" {
		if u, ok := byPrimary[hit.PrimaryMembershipID]; ok {
			return u, true
		}
	}
	if hit.BungieNetMembershipID != nil && *hit.BungieNetMembershipID != "" {
		if u, ok := byMember[*hit.BungieNetMembershipID]; ok {
			return u, true
		}
	}
	return User{}, false
}

func userToSearchResult(u User) api.SearchUserResult {
	chars := u.Characters
	if chars == nil {
		chars = []api.Character{}
	}
	uniqueName := strings.TrimSpace(u.UniqueName)
	if uniqueName == "" {
		uniqueName = strings.TrimSpace(u.DisplayName)
	}
	res := api.SearchUserResult{
		IsRecorded:          true,
		UserID:              new(u.ID),
		PrimaryMembershipID: u.PrimaryMembershipID,
		DisplayName:         u.DisplayName,
		UniqueName:          uniqueName,
		AlternateNames:      collectUserAlternateNames(u.Memberships),
		Characters:          new(chars),
	}
	if u.MemberID != "" {
		res.BungieNetMembershipID = new(u.MemberID)
	}
	if mType := resolveUserPrimaryMembershipType(u); mType > 0 {
		res.PrimaryMembershipType = new(mType)
	}
	return res
}

func mergeBungieWithUser(bungieHit api.SearchUserResult, u User) api.SearchUserResult {
	return mergeSearchResults(bungieHit, userToSearchResult(u))
}

func mergeSearchResults(a api.SearchUserResult, b api.SearchUserResult) api.SearchUserResult {
	out := a
	if b.IsRecorded {
		out.IsRecorded = true
	}
	if (out.UserID == nil || *out.UserID == "") && b.UserID != nil && *b.UserID != "" {
		out.UserID = new(*b.UserID)
	}
	if out.Characters == nil && b.Characters != nil {
		out.Characters = b.Characters
	}
	if out.PrimaryMembershipID == "" && b.PrimaryMembershipID != "" {
		out.PrimaryMembershipID = b.PrimaryMembershipID
	}
	if out.PrimaryMembershipType == nil && b.PrimaryMembershipType != nil {
		out.PrimaryMembershipType = new(*b.PrimaryMembershipType)
	}
	if (out.BungieNetMembershipID == nil || *out.BungieNetMembershipID == "") && b.BungieNetMembershipID != nil && *b.BungieNetMembershipID != "" {
		out.BungieNetMembershipID = new(*b.BungieNetMembershipID)
	}
	if (out.IconPath == nil || *out.IconPath == "") && b.IconPath != nil && *b.IconPath != "" {
		out.IconPath = new(*b.IconPath)
	}
	if out.DisplayName == "" && b.DisplayName != "" {
		out.DisplayName = b.DisplayName
	}
	out.UniqueName = preferUniqueName(out.UniqueName, b.UniqueName)
	out.AlternateNames = mergeUniqueStrings(out.AlternateNames, b.AlternateNames)
	return out
}

func preferUniqueName(a string, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	aHasDisc := strings.Contains(a, "#")
	bHasDisc := strings.Contains(b, "#")
	if aHasDisc && !bHasDisc {
		return a
	}
	if bHasDisc && !aHasDisc {
		return b
	}
	if a != "" {
		return a
	}
	return b
}

func rankTier(r api.SearchUserResult, query string) int {
	if strings.EqualFold(r.UniqueName, query) {
		if r.IsRecorded {
			return 0
		}
		return 1
	}
	if r.IsRecorded && strings.EqualFold(r.DisplayName, query) {
		return 2
	}
	if r.IsRecorded {
		return 3
	}
	return 4
}

func resolveUserPrimaryMembershipType(u User) int64 {
	for _, m := range u.Memberships {
		if m.ID == u.PrimaryMembershipID && m.Type > 0 {
			return m.Type
		}
	}
	if len(u.Memberships) > 0 && u.Memberships[0].Type > 0 {
		return u.Memberships[0].Type
	}
	return 0
}

func collectUserAlternateNames(memberships []Membership) []string {
	seen := make(map[string]struct{}, len(memberships))
	names := make([]string, 0, len(memberships))
	for _, m := range memberships {
		name := strings.TrimSpace(m.DisplayName)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func mergeUniqueStrings(a []string, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, item := range list {
			trimmed := strings.TrimSpace(item)
			if trimmed == "" {
				continue
			}
			if _, exists := seen[trimmed]; exists {
				continue
			}
			seen[trimmed] = struct{}{}
			out = append(out, trimmed)
		}
	}
	return out
}

func chunkUniqueStrings(values []string, size int) [][]string {
	if size <= 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, exists := seen[v]; exists {
			continue
		}
		seen[v] = struct{}{}
		unique = append(unique, v)
	}
	if len(unique) == 0 {
		return nil
	}
	chunks := make([][]string, 0, (len(unique)+size-1)/size)
	for i := 0; i < len(unique); i += size {
		end := i + size
		if end > len(unique) {
			end = len(unique)
		}
		chunks = append(chunks, unique[i:end])
	}
	return chunks
}

func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
