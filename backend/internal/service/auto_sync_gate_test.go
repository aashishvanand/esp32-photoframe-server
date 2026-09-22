package service

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/aitjcize/esp32-photoframe-server/backend/internal/model"
)

var gateTestDBCounter atomic.Int64

// setupGateDB returns an isolated in-memory DB with just the tables the
// auto-sync gate reads. Isolated (rather than the shared setupTestDB) so
// leftover settings from another test can't decide the gate.
func setupGateDB(t *testing.T) *gorm.DB {
	t.Helper()
	n := gateTestDBCounter.Add(1)
	db, err := gorm.Open(sqlite.Open(
		fmt.Sprintf("file:gate_test_%d?mode=memory&cache=shared", n)), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Setting{}, &model.Album{}))
	return db
}

func mkSourceAlbum(t *testing.T, db *gorm.DB, source, ext, kind string, enabled bool) {
	t.Helper()
	require.NoError(t, db.Create(&model.Album{
		Source: source, ExternalID: ext, Kind: kind, Name: ext, SyncEnabled: enabled,
	}).Error)
}

func immichGateService(t *testing.T, db *gorm.DB) *ImmichService {
	t.Helper()
	settings := NewSettingsService(db)
	require.NoError(t, settings.Set("immich_url", "http://immich.local"))
	require.NoError(t, settings.Set("immich_api_key", "key"))
	return NewImmichService(db, settings)
}

func synologyGateService(t *testing.T, db *gorm.DB) *SynologyService {
	t.Helper()
	settings := NewSettingsService(db)
	require.NoError(t, settings.Set("synology_url", "http://nas.local"))
	require.NoError(t, settings.Set("synology_account", "user"))
	require.NoError(t, settings.Set("synology_password", "pass"))
	return NewSynologyService(db, settings)
}

// Issue #54: picking sources through the multi-album picker writes album rows
// and never touches immich_source_mode/immich_album_id. Gating on the legacy
// pair made every picker-only setup look unconfigured, so auto-sync was
// skipped silently while manual Sync Now kept working.
func TestImmichAutoSyncConfiguredFromAlbumRows(t *testing.T) {
	db := setupGateDB(t)
	svc := immichGateService(t, db)

	// Memories only, via the picker. Legacy settings left at their defaults
	// (source_mode unset => "album", album_id empty) — the #54 repro.
	mkSourceAlbum(t, db, model.SourceImmich, model.ImmichVirtualMemories, model.AlbumKindVirtual, true)

	assert.True(t, svc.isAutoSyncConfigured(),
		"an enabled virtual album must count as configured")
}

func TestImmichAutoSyncConfiguredFromRealAlbumRow(t *testing.T) {
	db := setupGateDB(t)
	svc := immichGateService(t, db)

	mkSourceAlbum(t, db, model.SourceImmich, "album-uuid", model.AlbumKindReal, true)

	assert.True(t, svc.isAutoSyncConfigured())
}

// Everything deselected through the picker means "nothing to sync", not
// "fall back to whatever the legacy settings still say".
func TestImmichAutoSyncNotConfiguredWhenAllAlbumsDeselected(t *testing.T) {
	db := setupGateDB(t)
	svc := immichGateService(t, db)
	require.NoError(t, svc.settings.Set("immich_source_mode", ImmichModeMemories))

	mkSourceAlbum(t, db, model.SourceImmich, model.ImmichVirtualMemories, model.AlbumKindVirtual, false)

	assert.False(t, svc.isAutoSyncConfigured())
}

// Installs that predate the picker have no album rows at all; the legacy
// source_mode/album_id pair still decides for them.
func TestImmichAutoSyncLegacyFallbackWithoutAlbumRows(t *testing.T) {
	db := setupGateDB(t)
	svc := immichGateService(t, db)

	assert.False(t, svc.isAutoSyncConfigured(), "album mode with no album picked")

	require.NoError(t, svc.settings.Set("immich_album_id", "legacy-album"))
	assert.True(t, svc.isAutoSyncConfigured(), "album mode with the legacy album set")

	require.NoError(t, svc.settings.Set("immich_album_id", ""))
	require.NoError(t, svc.settings.Set("immich_source_mode", ImmichModeFavorites))
	assert.True(t, svc.isAutoSyncConfigured(), "non-album legacy mode needs no album")
}

func TestImmichAutoSyncNotConfiguredWithoutCredentials(t *testing.T) {
	db := setupGateDB(t)
	svc := immichGateService(t, db)
	mkSourceAlbum(t, db, model.SourceImmich, model.ImmichVirtualAll, model.AlbumKindVirtual, true)

	require.NoError(t, svc.settings.Set("immich_api_key", ""))
	assert.False(t, svc.isAutoSyncConfigured())
}

// Another source's albums must not satisfy this source's gate.
func TestImmichAutoSyncIgnoresOtherSourceAlbums(t *testing.T) {
	db := setupGateDB(t)
	svc := immichGateService(t, db)

	mkSourceAlbum(t, db, model.SourceSynologyPhotos, "7", model.AlbumKindReal, true)

	assert.False(t, svc.isAutoSyncConfigured())
}

// Synology reached the same dead end: SetSyncAlbums writes album rows, while
// the gate only ever read synology_album_id.
func TestSynologyAutoSyncConfiguredFromAlbumRows(t *testing.T) {
	db := setupGateDB(t)
	svc := synologyGateService(t, db)

	mkSourceAlbum(t, db, model.SourceSynologyPhotos, "42", model.AlbumKindReal, true)

	assert.True(t, svc.isAutoSyncConfigured())
}

func TestSynologyAutoSyncNotConfiguredWhenAllAlbumsDeselected(t *testing.T) {
	db := setupGateDB(t)
	svc := synologyGateService(t, db)

	mkSourceAlbum(t, db, model.SourceSynologyPhotos, "42", model.AlbumKindReal, false)

	assert.False(t, svc.isAutoSyncConfigured())
}

func TestSynologyAutoSyncLegacyFallbackWithoutAlbumRows(t *testing.T) {
	db := setupGateDB(t)
	svc := synologyGateService(t, db)

	assert.False(t, svc.isAutoSyncConfigured())

	require.NoError(t, svc.settings.Set("synology_album_id", "42"))
	assert.True(t, svc.isAutoSyncConfigured())
}

func TestSynologyAutoSyncNotConfiguredWithoutCredentials(t *testing.T) {
	db := setupGateDB(t)
	svc := synologyGateService(t, db)
	mkSourceAlbum(t, db, model.SourceSynologyPhotos, "42", model.AlbumKindReal, true)

	require.NoError(t, svc.settings.Set("synology_password", ""))
	assert.False(t, svc.isAutoSyncConfigured())
}
