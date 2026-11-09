package provider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/internal/domain"
)

func TestParseType(t *testing.T) {
	for in, want := range map[string]domain.ContentType{
		"video":     domain.ContentTypeVideo,
		"Video":     domain.ContentTypeVideo,
		"text":      domain.ContentTypeText,
		" article ": domain.ContentTypeText,
	} {
		got, err := ParseType(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	_, err := ParseType("podcast")
	assert.Error(t, err)
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{in: "", want: 0},
		{in: "15:30", want: 930},
		{in: "1:02:03", want: 3723},
		{in: "00:59", want: 59},
		{in: "15", wantErr: true},
		{in: "15:60", wantErr: true},
		{in: "aa:10", wantErr: true},
		{in: "-1:10", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseDuration(tt.in)
		if tt.wantErr {
			assert.Error(t, err, tt.in)
			continue
		}
		require.NoError(t, err, tt.in)
		assert.Equal(t, tt.want, got, tt.in)
	}
}

func TestParseDate(t *testing.T) {
	tests := map[string]time.Time{
		"2024-03-15T10:00:00Z":      time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC),
		"2024-03-15T13:00:00+03:00": time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC),
		"2024-03-15":                time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
		"2024-03-15 10:00:00":       time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC),
	}
	for in, want := range tests {
		got, err := ParseDate(in)
		require.NoError(t, err, in)
		assert.True(t, want.Equal(got), "%s: got %s", in, got)
	}

	_, err := ParseDate("15/03/2024")
	assert.Error(t, err)
}

func TestCleanTags(t *testing.T) {
	assert.Equal(t, []string{"go", "api"}, CleanTags([]string{" Go ", "", "api", "go"}))
	assert.Equal(t, []string{}, CleanTags(nil))
}

func TestValidate(t *testing.T) {
	valid := domain.Content{ExternalID: "v1", Title: "t", PublishedAt: time.Now()}
	require.NoError(t, Validate(valid))

	noID := valid
	noID.ExternalID = " "
	assert.Error(t, Validate(noID))

	noTitle := valid
	noTitle.Title = ""
	assert.Error(t, Validate(noTitle))

	negative := valid
	negative.Metrics.Views = -1
	assert.Error(t, Validate(negative))
}
