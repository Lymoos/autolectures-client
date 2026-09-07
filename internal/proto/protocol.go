package proto

const ProtocolVersion = 2

const (
	WsPath          = "/ws/node"
	ApiAuthRegister = "/api/v1/auth/register"
	ApiAuthLogin    = "/api/v1/auth/login"
	ApiAuthMe       = "/api/v1/auth/me"
	ApiSyncSettings = "/api/v1/sync/settings"
	ApiSyncLinks    = "/api/v1/sync/links"
	ApiTelegramCode = "/api/v1/telegram/link-code"
	ApiTelegramStat = "/api/v1/telegram/status"
	// Пресет ссылок на курсы СДО для учебной группы: один раз собирает
	// староста-администратор, остальным приходит готовым.
	ApiSdoPreset = "/api/v1/sdo/preset"
	// Пропущенные пары — для общей «стены» по группе.
	ApiWallMiss = "/api/v1/wall/miss"
	// Сводка пропусков по своей группе: week | month | semester.
	ApiWall    = "/api/v1/wall"
	ApiStats   = "/api/v1/stats"
	ApiVersion = "/api/v1/version"
	ApiHealth  = "/health"
)

const (
	C2SAuth          = "AUTH"
	C2SCurrentStatus = "CURRENT_STATUS"
	C2SHeartbeatAck  = "HEARTBEAT_ACK"
	C2STokenResult   = "TOKEN_RESULT"
	C2SLiveUrlUpdate = "LIVE_URL_UPDATE"
	C2SStreamStarted = "STREAM_STARTED"
	C2SStreamStopped = "STREAM_STOPPED"
	C2SNotify        = "NOTIFY"
	C2SLinkUpsert    = "LINK_UPSERT"
)

const (
	S2CAuthOk          = "AUTH_OK"
	S2CAuthError       = "AUTH_ERROR"
	S2CStartSession    = "START_SESSION"
	S2CStopSession     = "STOP_SESSION"
	S2CGetStatus       = "GET_STATUS"
	S2CShutdown        = "SHUTDOWN"
	S2CSettingsUpdated = "SETTINGS_UPDATED"
)

const (
	TokenSuccess   = "SUCCESS"
	TokenNeedsAuth = "NEEDS_AUTH"
	TokenRetry     = "RETRY"
	TokenTimeout   = "TIMEOUT"
	TokenError     = "ERROR"
)

const (
	EventLectureStarted    = "LECTURE_STARTED"
	EventLectureStopped    = "LECTURE_STOPPED"
	EventPresenceConfirmed = "PRESENCE_CONFIRMED"
	EventAttendanceMarked  = "ATTENDANCE_MARKED"
	EventEmailParsed       = "EMAIL_PARSED"
	EventLinkMissing       = "LINK_MISSING"
	EventUpdateAvailable   = "UPDATE_AVAILABLE"
	EventAuthRequired      = "AUTH_REQUIRED"
	EventSessionError      = "SESSION_ERROR"
)

const (
	LinkPending = "PENDING"
	LinkLive    = "LIVE"
	LinkMarked  = "MARKED"
	LinkMissed  = "MISSED"
	LinkDone    = "DONE"
)
