package nosconst


const (
    HZ = iota
)

type Location  int
type Acl int


const(
    PRIVATE = iota
    PUBLICREAD
)

const (
	DEFAULT_MAXBUFFERSIZE = 1024 * 1024
	MAX_FILESIZE          = 100 * 1024 * 1024
	MIN_FILESIZE          = 16 * 1024
	MAX_FILENUMBER        = 1000
	DEFAULTVALUE          = 1000
	MAX_DELETEBODY        = 2 * 1024 * 1024

	RFC1123_NOS          = "Mon, 02 Jan 2006 15:04:05 Asia/Shanghai"
	RFC1123_GMT          = "Mon, 02 Jan 2006 15:04:05 GMT"
	CONTENT_LENGTH       = "Content-Length"
	CONTENT_TYPE         = "Content-Type"
	CONTENT_MD5          = "Content-Md5"
	LAST_MODIFIED        = "Last-Modified"
	USER_AGENT           = "User-Agent"
	DATE                 = "Date"
	AUTHORIZATION        = "Authorization"
	RANGE                = "Range"
	IfMODIFYSINCE        = "If-Modified-Since"
	LIST_PREFIX          = "prefix"
	LIST_DELIMITER       = "delimiter"
	LIST_MARKER          = "marker"
	LIST_MAXKEYS         = "max-keys"
	UPLOADID             = "uploadId"
	MAX_PARTS            = "max-parts"
	PARTNUMBER           = "partNumber"
	UPLOADS              = "uploads"
	PART_NUMBER_MARKER   = "part-number-marker"
	LIST_KEY_MARKER      = "key-marker"
	LIST_MAX_UPLOADS     = "max-uploads"
	LIST_UPLOADID_MARKER = "upload-id-marker"

	ETAG                     = "Etag"
	NOS_USER_METADATA_PREFIX = "X-Nos-Meta-"
	NOS_ENTITY_TYPE          = "X-Nos-Entity-Type"
	NOS_VERSION_ID           = "X-Nos-Version-Id"
	X_NOS_OBJECT_NAME        = "X-Nos-Object-Name"
	X_NOS_REQUEST_ID         = "X-Nos-Request-Id"
	X_NOS_OBJECT_MD5         = "X-Nos-Object-Md5"
	X_NOS_COPY_SOURCE        = "x-nos-copy-source"
	X_NOS_MOVE_SOURCE        = "x-nos-move-source"
    X_NOS_ACL                = "x-nos-acl"

	ORIG_CONTENT_MD5              = "Content-MD5"
	ORIG_ETAG                     = "ETag"
	ORIG_NOS_USER_METADATA_PREFIX = "x-nos-meta-"
	ORIG_NOS_VERSION_ID           = "x-nos-version-id"
	ORIG_X_NOS_OBJECT_NAME        = "x-nos-object-name"
	ORIG_X_NOS_REQUEST_ID         = "x-nos-request-id"
	ORIG_X_NOS_OBJECT_MD5         = "x-nos-Object-md5"

	SDKNAME                       = "nos-golang-sdk"
	VERSION                       = "1.0.0"

	JSON_TYPE = "json"
	XML_TYPE  = "xml"
)
