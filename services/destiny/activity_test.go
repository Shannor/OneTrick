package destiny

import (
	"context"
	"errors"
	"net/http"
	"oneTrick/clients/bungie"
	"testing"
)

type fakeActivityBungieClient struct {
	bungie.ClientWithResponsesInterface
	pgcrResp *bungie.Destiny2GetPostGameCarnageReportResponse
	pgcrErr  error
	lastID   int64
}

func (f *fakeActivityBungieClient) Destiny2GetPostGameCarnageReportWithResponse(
	_ context.Context,
	activityID int64,
	_ ...bungie.RequestEditorFn,
) (*bungie.Destiny2GetPostGameCarnageReportResponse, error) {
	f.lastID = activityID
	return f.pgcrResp, f.pgcrErr
}

func TestGetActivity_ErrorMappingAndSuccess(t *testing.T) {
	t.Run("invalid activity ID returns parse error without calling Bungie", func(t *testing.T) {
		client := &fakeActivityBungieClient{}
		svc := &service{Client: client}

		_, _, err := svc.GetActivity(context.Background(), "not-a-number")
		if err == nil {
			t.Fatal("expected error for non-numeric activity ID")
		}
		if client.lastID != 0 {
			t.Fatalf("expected Bungie client not to be called, got lastID=%d", client.lastID)
		}
	})

	t.Run("HTTP 404 maps to ErrActivityNotFound", func(t *testing.T) {
		svc := &service{
			Client: &fakeActivityBungieClient{
				pgcrResp: &bungie.Destiny2GetPostGameCarnageReportResponse{
					HTTPResponse: &http.Response{StatusCode: http.StatusNotFound},
				},
			},
		}

		_, _, err := svc.GetActivity(context.Background(), "123456")
		if !errors.Is(err, ErrActivityNotFound) {
			t.Fatalf("expected ErrActivityNotFound, got %v", err)
		}
	})

	t.Run("Bungie ErrorCode 1653 maps to ErrActivityNotFound", func(t *testing.T) {
		svc := &service{
			Client: &fakeActivityBungieClient{
				pgcrResp: &bungie.Destiny2GetPostGameCarnageReportResponse{
					HTTPResponse: &http.Response{StatusCode: http.StatusOK},
					JSON200: &bungie.DestinyHistoricalStatsDestinyPostGameCarnageReportData{
						ErrorCode: new(int32(bungieErrorCodePGCRNotFound)),
					},
				},
			},
		}

		_, _, err := svc.GetActivity(context.Background(), "123456")
		if !errors.Is(err, ErrActivityNotFound) {
			t.Fatalf("expected ErrActivityNotFound for ErrorCode 1653, got %v", err)
		}
	})

	t.Run("HTTP 503 maps to ErrDestinyServerDown", func(t *testing.T) {
		svc := &service{
			Client: &fakeActivityBungieClient{
				pgcrResp: &bungie.Destiny2GetPostGameCarnageReportResponse{
					HTTPResponse: &http.Response{StatusCode: http.StatusServiceUnavailable},
				},
			},
		}

		_, _, err := svc.GetActivity(context.Background(), "123456")
		if !errors.Is(err, ErrDestinyServerDown) {
			t.Fatalf("expected ErrDestinyServerDown, got %v", err)
		}
	})

	t.Run("Bungie ErrorCode 5 maps to ErrDestinyServerDown", func(t *testing.T) {
		svc := &service{
			Client: &fakeActivityBungieClient{
				pgcrResp: &bungie.Destiny2GetPostGameCarnageReportResponse{
					HTTPResponse: &http.Response{StatusCode: http.StatusOK},
					JSON200: &bungie.DestinyHistoricalStatsDestinyPostGameCarnageReportData{
						ErrorCode: new(int32(bungieErrorCodeSystemDisabled)),
					},
				},
			},
		}

		_, _, err := svc.GetActivity(context.Background(), "123456")
		if !errors.Is(err, ErrDestinyServerDown) {
			t.Fatalf("expected ErrDestinyServerDown for ErrorCode 5, got %v", err)
		}
	})

	t.Run("returns PGCR data and transformed teams on 200 OK", func(t *testing.T) {
		client := &fakeActivityBungieClient{
			pgcrResp: &bungie.Destiny2GetPostGameCarnageReportResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.DestinyHistoricalStatsDestinyPostGameCarnageReportData{
					ErrorCode: new(int32(1)),
					PostGameCarnageReportData: &bungie.PostGameCarnageReportData{
						Teams: &[]bungie.TeamEntry{
							{
								TeamID:   new(int32(18)),
								TeamName: new("Alpha"),
								Standing: &bungie.HistoricalStatsValue{
									Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{
										Value:        new(float64(0)),
										DisplayValue: new("Victory"),
									},
								},
								Score: &bungie.HistoricalStatsValue{
									Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{
										Value:        new(float64(5)),
										DisplayValue: new("5"),
									},
								},
							},
						},
					},
				},
			},
		}
		svc := &service{Client: client}

		data, teams, err := svc.GetActivity(context.Background(), "987654321")
		if err != nil {
			t.Fatalf("GetActivity returned unexpected error: %v", err)
		}
		if client.lastID != 987654321 {
			t.Fatalf("expected activityID 987654321, got %d", client.lastID)
		}
		if data == nil {
			t.Fatal("expected non-nil PGCR data")
		}
		if len(teams) != 1 || teams[0].ID != "18" || teams[0].Score != "5" || teams[0].Standing != "Victory" {
			t.Fatalf("unexpected transformed teams: %+v", teams)
		}
	})
}
