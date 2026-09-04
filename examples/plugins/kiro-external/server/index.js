// @bun
var __commonJS = (cb, mod) => () => (mod || cb((mod = { exports: {} }).exports, mod), mod.exports);
var __require = import.meta.require;

// node_modules/undici/lib/core/symbols.js
var require_symbols = __commonJS((exports, module) => {
  module.exports = {
    kClose: Symbol("close"),
    kDestroy: Symbol("destroy"),
    kDispatch: Symbol("dispatch"),
    kUrl: Symbol("url"),
    kWriting: Symbol("writing"),
    kResuming: Symbol("resuming"),
    kQueue: Symbol("queue"),
    kConnect: Symbol("connect"),
    kConnecting: Symbol("connecting"),
    kKeepAliveDefaultTimeout: Symbol("default keep alive timeout"),
    kKeepAliveMaxTimeout: Symbol("max keep alive timeout"),
    kKeepAliveTimeoutThreshold: Symbol("keep alive timeout threshold"),
    kKeepAliveTimeoutValue: Symbol("keep alive timeout"),
    kKeepAlive: Symbol("keep alive"),
    kHeadersTimeout: Symbol("headers timeout"),
    kBodyTimeout: Symbol("body timeout"),
    kServerName: Symbol("server name"),
    kLocalAddress: Symbol("local address"),
    kHost: Symbol("host"),
    kNoRef: Symbol("no ref"),
    kBodyUsed: Symbol("used"),
    kBody: Symbol("abstracted request body"),
    kRunning: Symbol("running"),
    kBlocking: Symbol("blocking"),
    kPending: Symbol("pending"),
    kSize: Symbol("size"),
    kBusy: Symbol("busy"),
    kQueued: Symbol("queued"),
    kFree: Symbol("free"),
    kConnected: Symbol("connected"),
    kClosed: Symbol("closed"),
    kNeedDrain: Symbol("need drain"),
    kReset: Symbol("reset"),
    kDestroyed: Symbol.for("nodejs.stream.destroyed"),
    kResume: Symbol("resume"),
    kOnError: Symbol("on error"),
    kMaxHeadersSize: Symbol("max headers size"),
    kRunningIdx: Symbol("running index"),
    kPendingIdx: Symbol("pending index"),
    kError: Symbol("error"),
    kClients: Symbol("clients"),
    kClient: Symbol("client"),
    kParser: Symbol("parser"),
    kOnDestroyed: Symbol("destroy callbacks"),
    kPipelining: Symbol("pipelining"),
    kSocket: Symbol("socket"),
    kHostHeader: Symbol("host header"),
    kConnector: Symbol("connector"),
    kStrictContentLength: Symbol("strict content length"),
    kMaxRedirections: Symbol("maxRedirections"),
    kMaxRequests: Symbol("maxRequestsPerClient"),
    kProxy: Symbol("proxy agent options"),
    kCounter: Symbol("socket request counter"),
    kInterceptors: Symbol("dispatch interceptors"),
    kMaxResponseSize: Symbol("max response size"),
    kHTTP2Session: Symbol("http2Session"),
    kHTTP2SessionState: Symbol("http2Session state"),
    kRetryHandlerDefaultRetry: Symbol("retry agent default retry"),
    kConstruct: Symbol("constructable"),
    kListeners: Symbol("listeners"),
    kHTTPContext: Symbol("http context"),
    kMaxConcurrentStreams: Symbol("max concurrent streams"),
    kNoProxyAgent: Symbol("no proxy agent"),
    kHttpProxyAgent: Symbol("http proxy agent"),
    kHttpsProxyAgent: Symbol("https proxy agent")
  };
});

// node_modules/undici/lib/core/errors.js
var require_errors = __commonJS((exports, module) => {
  var kUndiciError = Symbol.for("undici.error.UND_ERR");

  class UndiciError extends Error {
    constructor(message) {
      super(message);
      this.name = "UndiciError";
      this.code = "UND_ERR";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kUndiciError] === true;
    }
    [kUndiciError] = true;
  }
  var kConnectTimeoutError = Symbol.for("undici.error.UND_ERR_CONNECT_TIMEOUT");

  class ConnectTimeoutError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "ConnectTimeoutError";
      this.message = message || "Connect Timeout Error";
      this.code = "UND_ERR_CONNECT_TIMEOUT";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kConnectTimeoutError] === true;
    }
    [kConnectTimeoutError] = true;
  }
  var kHeadersTimeoutError = Symbol.for("undici.error.UND_ERR_HEADERS_TIMEOUT");

  class HeadersTimeoutError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "HeadersTimeoutError";
      this.message = message || "Headers Timeout Error";
      this.code = "UND_ERR_HEADERS_TIMEOUT";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kHeadersTimeoutError] === true;
    }
    [kHeadersTimeoutError] = true;
  }
  var kHeadersOverflowError = Symbol.for("undici.error.UND_ERR_HEADERS_OVERFLOW");

  class HeadersOverflowError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "HeadersOverflowError";
      this.message = message || "Headers Overflow Error";
      this.code = "UND_ERR_HEADERS_OVERFLOW";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kHeadersOverflowError] === true;
    }
    [kHeadersOverflowError] = true;
  }
  var kBodyTimeoutError = Symbol.for("undici.error.UND_ERR_BODY_TIMEOUT");

  class BodyTimeoutError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "BodyTimeoutError";
      this.message = message || "Body Timeout Error";
      this.code = "UND_ERR_BODY_TIMEOUT";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kBodyTimeoutError] === true;
    }
    [kBodyTimeoutError] = true;
  }
  var kResponseStatusCodeError = Symbol.for("undici.error.UND_ERR_RESPONSE_STATUS_CODE");

  class ResponseStatusCodeError extends UndiciError {
    constructor(message, statusCode, headers, body) {
      super(message);
      this.name = "ResponseStatusCodeError";
      this.message = message || "Response Status Code Error";
      this.code = "UND_ERR_RESPONSE_STATUS_CODE";
      this.body = body;
      this.status = statusCode;
      this.statusCode = statusCode;
      this.headers = headers;
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kResponseStatusCodeError] === true;
    }
    [kResponseStatusCodeError] = true;
  }
  var kInvalidArgumentError = Symbol.for("undici.error.UND_ERR_INVALID_ARG");

  class InvalidArgumentError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "InvalidArgumentError";
      this.message = message || "Invalid Argument Error";
      this.code = "UND_ERR_INVALID_ARG";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kInvalidArgumentError] === true;
    }
    [kInvalidArgumentError] = true;
  }
  var kInvalidReturnValueError = Symbol.for("undici.error.UND_ERR_INVALID_RETURN_VALUE");

  class InvalidReturnValueError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "InvalidReturnValueError";
      this.message = message || "Invalid Return Value Error";
      this.code = "UND_ERR_INVALID_RETURN_VALUE";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kInvalidReturnValueError] === true;
    }
    [kInvalidReturnValueError] = true;
  }
  var kAbortError = Symbol.for("undici.error.UND_ERR_ABORT");

  class AbortError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "AbortError";
      this.message = message || "The operation was aborted";
      this.code = "UND_ERR_ABORT";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kAbortError] === true;
    }
    [kAbortError] = true;
  }
  var kRequestAbortedError = Symbol.for("undici.error.UND_ERR_ABORTED");

  class RequestAbortedError extends AbortError {
    constructor(message) {
      super(message);
      this.name = "AbortError";
      this.message = message || "Request aborted";
      this.code = "UND_ERR_ABORTED";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kRequestAbortedError] === true;
    }
    [kRequestAbortedError] = true;
  }
  var kInformationalError = Symbol.for("undici.error.UND_ERR_INFO");

  class InformationalError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "InformationalError";
      this.message = message || "Request information";
      this.code = "UND_ERR_INFO";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kInformationalError] === true;
    }
    [kInformationalError] = true;
  }
  var kRequestContentLengthMismatchError = Symbol.for("undici.error.UND_ERR_REQ_CONTENT_LENGTH_MISMATCH");

  class RequestContentLengthMismatchError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "RequestContentLengthMismatchError";
      this.message = message || "Request body length does not match content-length header";
      this.code = "UND_ERR_REQ_CONTENT_LENGTH_MISMATCH";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kRequestContentLengthMismatchError] === true;
    }
    [kRequestContentLengthMismatchError] = true;
  }
  var kResponseContentLengthMismatchError = Symbol.for("undici.error.UND_ERR_RES_CONTENT_LENGTH_MISMATCH");

  class ResponseContentLengthMismatchError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "ResponseContentLengthMismatchError";
      this.message = message || "Response body length does not match content-length header";
      this.code = "UND_ERR_RES_CONTENT_LENGTH_MISMATCH";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kResponseContentLengthMismatchError] === true;
    }
    [kResponseContentLengthMismatchError] = true;
  }
  var kClientDestroyedError = Symbol.for("undici.error.UND_ERR_DESTROYED");

  class ClientDestroyedError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "ClientDestroyedError";
      this.message = message || "The client is destroyed";
      this.code = "UND_ERR_DESTROYED";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kClientDestroyedError] === true;
    }
    [kClientDestroyedError] = true;
  }
  var kClientClosedError = Symbol.for("undici.error.UND_ERR_CLOSED");

  class ClientClosedError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "ClientClosedError";
      this.message = message || "The client is closed";
      this.code = "UND_ERR_CLOSED";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kClientClosedError] === true;
    }
    [kClientClosedError] = true;
  }
  var kSocketError = Symbol.for("undici.error.UND_ERR_SOCKET");

  class SocketError extends UndiciError {
    constructor(message, socket) {
      super(message);
      this.name = "SocketError";
      this.message = message || "Socket error";
      this.code = "UND_ERR_SOCKET";
      this.socket = socket;
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kSocketError] === true;
    }
    [kSocketError] = true;
  }
  var kNotSupportedError = Symbol.for("undici.error.UND_ERR_NOT_SUPPORTED");

  class NotSupportedError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "NotSupportedError";
      this.message = message || "Not supported error";
      this.code = "UND_ERR_NOT_SUPPORTED";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kNotSupportedError] === true;
    }
    [kNotSupportedError] = true;
  }
  var kBalancedPoolMissingUpstreamError = Symbol.for("undici.error.UND_ERR_BPL_MISSING_UPSTREAM");

  class BalancedPoolMissingUpstreamError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "MissingUpstreamError";
      this.message = message || "No upstream has been added to the BalancedPool";
      this.code = "UND_ERR_BPL_MISSING_UPSTREAM";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kBalancedPoolMissingUpstreamError] === true;
    }
    [kBalancedPoolMissingUpstreamError] = true;
  }
  var kHTTPParserError = Symbol.for("undici.error.UND_ERR_HTTP_PARSER");

  class HTTPParserError extends Error {
    constructor(message, code, data) {
      super(message);
      this.name = "HTTPParserError";
      this.code = code ? `HPE_${code}` : undefined;
      this.data = data ? data.toString() : undefined;
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kHTTPParserError] === true;
    }
    [kHTTPParserError] = true;
  }
  var kResponseExceededMaxSizeError = Symbol.for("undici.error.UND_ERR_RES_EXCEEDED_MAX_SIZE");

  class ResponseExceededMaxSizeError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "ResponseExceededMaxSizeError";
      this.message = message || "Response content exceeded max size";
      this.code = "UND_ERR_RES_EXCEEDED_MAX_SIZE";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kResponseExceededMaxSizeError] === true;
    }
    [kResponseExceededMaxSizeError] = true;
  }
  var kRequestRetryError = Symbol.for("undici.error.UND_ERR_REQ_RETRY");

  class RequestRetryError extends UndiciError {
    constructor(message, code, { headers, data }) {
      super(message);
      this.name = "RequestRetryError";
      this.message = message || "Request retry error";
      this.code = "UND_ERR_REQ_RETRY";
      this.statusCode = code;
      this.data = data;
      this.headers = headers;
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kRequestRetryError] === true;
    }
    [kRequestRetryError] = true;
  }
  var kResponseError = Symbol.for("undici.error.UND_ERR_RESPONSE");

  class ResponseError extends UndiciError {
    constructor(message, code, { headers, data }) {
      super(message);
      this.name = "ResponseError";
      this.message = message || "Response error";
      this.code = "UND_ERR_RESPONSE";
      this.statusCode = code;
      this.data = data;
      this.headers = headers;
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kResponseError] === true;
    }
    [kResponseError] = true;
  }
  var kSecureProxyConnectionError = Symbol.for("undici.error.UND_ERR_PRX_TLS");

  class SecureProxyConnectionError extends UndiciError {
    constructor(cause, message, options) {
      super(message, { cause, ...options ?? {} });
      this.name = "SecureProxyConnectionError";
      this.message = message || "Secure Proxy Connection failed";
      this.code = "UND_ERR_PRX_TLS";
      this.cause = cause;
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kSecureProxyConnectionError] === true;
    }
    [kSecureProxyConnectionError] = true;
  }
  var kMessageSizeExceededError = Symbol.for("undici.error.UND_ERR_WS_MESSAGE_SIZE_EXCEEDED");

  class MessageSizeExceededError extends UndiciError {
    constructor(message) {
      super(message);
      this.name = "MessageSizeExceededError";
      this.message = message || "Max decompressed message size exceeded";
      this.code = "UND_ERR_WS_MESSAGE_SIZE_EXCEEDED";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kMessageSizeExceededError] === true;
    }
    get [kMessageSizeExceededError]() {
      return true;
    }
  }
  module.exports = {
    AbortError,
    HTTPParserError,
    UndiciError,
    HeadersTimeoutError,
    HeadersOverflowError,
    BodyTimeoutError,
    RequestContentLengthMismatchError,
    ConnectTimeoutError,
    ResponseStatusCodeError,
    InvalidArgumentError,
    InvalidReturnValueError,
    RequestAbortedError,
    ClientDestroyedError,
    ClientClosedError,
    InformationalError,
    SocketError,
    NotSupportedError,
    ResponseContentLengthMismatchError,
    BalancedPoolMissingUpstreamError,
    ResponseExceededMaxSizeError,
    RequestRetryError,
    ResponseError,
    SecureProxyConnectionError,
    MessageSizeExceededError
  };
});

// node_modules/undici/lib/core/constants.js
var require_constants = __commonJS((exports, module) => {
  var headerNameLowerCasedRecord = {};
  var wellknownHeaderNames = [
    "Accept",
    "Accept-Encoding",
    "Accept-Language",
    "Accept-Ranges",
    "Access-Control-Allow-Credentials",
    "Access-Control-Allow-Headers",
    "Access-Control-Allow-Methods",
    "Access-Control-Allow-Origin",
    "Access-Control-Expose-Headers",
    "Access-Control-Max-Age",
    "Access-Control-Request-Headers",
    "Access-Control-Request-Method",
    "Age",
    "Allow",
    "Alt-Svc",
    "Alt-Used",
    "Authorization",
    "Cache-Control",
    "Clear-Site-Data",
    "Connection",
    "Content-Disposition",
    "Content-Encoding",
    "Content-Language",
    "Content-Length",
    "Content-Location",
    "Content-Range",
    "Content-Security-Policy",
    "Content-Security-Policy-Report-Only",
    "Content-Type",
    "Cookie",
    "Cross-Origin-Embedder-Policy",
    "Cross-Origin-Opener-Policy",
    "Cross-Origin-Resource-Policy",
    "Date",
    "Device-Memory",
    "Downlink",
    "ECT",
    "ETag",
    "Expect",
    "Expect-CT",
    "Expires",
    "Forwarded",
    "From",
    "Host",
    "If-Match",
    "If-Modified-Since",
    "If-None-Match",
    "If-Range",
    "If-Unmodified-Since",
    "Keep-Alive",
    "Last-Modified",
    "Link",
    "Location",
    "Max-Forwards",
    "Origin",
    "Permissions-Policy",
    "Pragma",
    "Proxy-Authenticate",
    "Proxy-Authorization",
    "RTT",
    "Range",
    "Referer",
    "Referrer-Policy",
    "Refresh",
    "Retry-After",
    "Sec-WebSocket-Accept",
    "Sec-WebSocket-Extensions",
    "Sec-WebSocket-Key",
    "Sec-WebSocket-Protocol",
    "Sec-WebSocket-Version",
    "Server",
    "Server-Timing",
    "Service-Worker-Allowed",
    "Service-Worker-Navigation-Preload",
    "Set-Cookie",
    "SourceMap",
    "Strict-Transport-Security",
    "Supports-Loading-Mode",
    "TE",
    "Timing-Allow-Origin",
    "Trailer",
    "Transfer-Encoding",
    "Upgrade",
    "Upgrade-Insecure-Requests",
    "User-Agent",
    "Vary",
    "Via",
    "WWW-Authenticate",
    "X-Content-Type-Options",
    "X-DNS-Prefetch-Control",
    "X-Frame-Options",
    "X-Permitted-Cross-Domain-Policies",
    "X-Powered-By",
    "X-Requested-With",
    "X-XSS-Protection"
  ];
  for (let i = 0;i < wellknownHeaderNames.length; ++i) {
    const key = wellknownHeaderNames[i];
    const lowerCasedKey = key.toLowerCase();
    headerNameLowerCasedRecord[key] = headerNameLowerCasedRecord[lowerCasedKey] = lowerCasedKey;
  }
  Object.setPrototypeOf(headerNameLowerCasedRecord, null);
  module.exports = {
    wellknownHeaderNames,
    headerNameLowerCasedRecord
  };
});

// node_modules/undici/lib/core/tree.js
var require_tree = __commonJS((exports, module) => {
  var {
    wellknownHeaderNames,
    headerNameLowerCasedRecord
  } = require_constants();

  class TstNode {
    value = null;
    left = null;
    middle = null;
    right = null;
    code;
    constructor(key, value, index) {
      if (index === undefined || index >= key.length) {
        throw new TypeError("Unreachable");
      }
      const code = this.code = key.charCodeAt(index);
      if (code > 127) {
        throw new TypeError("key must be ascii string");
      }
      if (key.length !== ++index) {
        this.middle = new TstNode(key, value, index);
      } else {
        this.value = value;
      }
    }
    add(key, value) {
      const length = key.length;
      if (length === 0) {
        throw new TypeError("Unreachable");
      }
      let index = 0;
      let node = this;
      while (true) {
        const code = key.charCodeAt(index);
        if (code > 127) {
          throw new TypeError("key must be ascii string");
        }
        if (node.code === code) {
          if (length === ++index) {
            node.value = value;
            break;
          } else if (node.middle !== null) {
            node = node.middle;
          } else {
            node.middle = new TstNode(key, value, index);
            break;
          }
        } else if (node.code < code) {
          if (node.left !== null) {
            node = node.left;
          } else {
            node.left = new TstNode(key, value, index);
            break;
          }
        } else if (node.right !== null) {
          node = node.right;
        } else {
          node.right = new TstNode(key, value, index);
          break;
        }
      }
    }
    search(key) {
      const keylength = key.length;
      let index = 0;
      let node = this;
      while (node !== null && index < keylength) {
        let code = key[index];
        if (code <= 90 && code >= 65) {
          code |= 32;
        }
        while (node !== null) {
          if (code === node.code) {
            if (keylength === ++index) {
              return node;
            }
            node = node.middle;
            break;
          }
          node = node.code < code ? node.left : node.right;
        }
      }
      return null;
    }
  }

  class TernarySearchTree {
    node = null;
    insert(key, value) {
      if (this.node === null) {
        this.node = new TstNode(key, value, 0);
      } else {
        this.node.add(key, value);
      }
    }
    lookup(key) {
      return this.node?.search(key)?.value ?? null;
    }
  }
  var tree = new TernarySearchTree;
  for (let i = 0;i < wellknownHeaderNames.length; ++i) {
    const key = headerNameLowerCasedRecord[wellknownHeaderNames[i]];
    tree.insert(key, key);
  }
  module.exports = {
    TernarySearchTree,
    tree
  };
});

// node_modules/undici/lib/core/util.js
var require_util = __commonJS((exports, module) => {
  var assert = __require("assert");
  var { kDestroyed, kBodyUsed, kListeners, kBody } = require_symbols();
  var { IncomingMessage } = __require("http");
  var stream = __require("stream");
  var net = __require("net");
  var { Blob: Blob2 } = __require("buffer");
  var nodeUtil = __require("util");
  var { stringify } = __require("querystring");
  var { EventEmitter: EE } = __require("events");
  var { InvalidArgumentError } = require_errors();
  var { headerNameLowerCasedRecord } = require_constants();
  var { tree } = require_tree();
  var [nodeMajor, nodeMinor] = process.versions.node.split(".").map((v) => Number(v));

  class BodyAsyncIterable {
    constructor(body) {
      this[kBody] = body;
      this[kBodyUsed] = false;
    }
    async* [Symbol.asyncIterator]() {
      assert(!this[kBodyUsed], "disturbed");
      this[kBodyUsed] = true;
      yield* this[kBody];
    }
  }
  function wrapRequestBody(body) {
    if (isStream(body)) {
      if (bodyLength(body) === 0) {
        body.on("data", function() {
          assert(false);
        });
      }
      if (typeof body.readableDidRead !== "boolean") {
        body[kBodyUsed] = false;
        EE.prototype.on.call(body, "data", function() {
          this[kBodyUsed] = true;
        });
      }
      return body;
    } else if (body && typeof body.pipeTo === "function") {
      return new BodyAsyncIterable(body);
    } else if (body && typeof body !== "string" && !ArrayBuffer.isView(body) && isIterable(body)) {
      return new BodyAsyncIterable(body);
    } else {
      return body;
    }
  }
  function nop() {}
  function isStream(obj) {
    return obj && typeof obj === "object" && typeof obj.pipe === "function" && typeof obj.on === "function";
  }
  function isBlobLike(object) {
    if (object === null) {
      return false;
    } else if (object instanceof Blob2) {
      return true;
    } else if (typeof object !== "object") {
      return false;
    } else {
      const sTag = object[Symbol.toStringTag];
      return (sTag === "Blob" || sTag === "File") && (("stream" in object) && typeof object.stream === "function" || ("arrayBuffer" in object) && typeof object.arrayBuffer === "function");
    }
  }
  function buildURL(url, queryParams) {
    if (url.includes("?") || url.includes("#")) {
      throw new Error('Query params cannot be passed when url already contains "?" or "#".');
    }
    const stringified = stringify(queryParams);
    if (stringified) {
      url += "?" + stringified;
    }
    return url;
  }
  function isValidPort(port) {
    const value = parseInt(port, 10);
    return value === Number(port) && value >= 0 && value <= 65535;
  }
  function isHttpOrHttpsPrefixed(value) {
    return value != null && value[0] === "h" && value[1] === "t" && value[2] === "t" && value[3] === "p" && (value[4] === ":" || value[4] === "s" && value[5] === ":");
  }
  function parseURL(url) {
    if (typeof url === "string") {
      url = new URL(url);
      if (!isHttpOrHttpsPrefixed(url.origin || url.protocol)) {
        throw new InvalidArgumentError("Invalid URL protocol: the URL must start with `http:` or `https:`.");
      }
      return url;
    }
    if (!url || typeof url !== "object") {
      throw new InvalidArgumentError("Invalid URL: The URL argument must be a non-null object.");
    }
    if (!(url instanceof URL)) {
      if (url.port != null && url.port !== "" && isValidPort(url.port) === false) {
        throw new InvalidArgumentError("Invalid URL: port must be a valid integer or a string representation of an integer.");
      }
      if (url.path != null && typeof url.path !== "string") {
        throw new InvalidArgumentError("Invalid URL path: the path must be a string or null/undefined.");
      }
      if (url.pathname != null && typeof url.pathname !== "string") {
        throw new InvalidArgumentError("Invalid URL pathname: the pathname must be a string or null/undefined.");
      }
      if (url.hostname != null && typeof url.hostname !== "string") {
        throw new InvalidArgumentError("Invalid URL hostname: the hostname must be a string or null/undefined.");
      }
      if (url.origin != null && typeof url.origin !== "string") {
        throw new InvalidArgumentError("Invalid URL origin: the origin must be a string or null/undefined.");
      }
      if (!isHttpOrHttpsPrefixed(url.origin || url.protocol)) {
        throw new InvalidArgumentError("Invalid URL protocol: the URL must start with `http:` or `https:`.");
      }
      const port = url.port != null ? url.port : url.protocol === "https:" ? 443 : 80;
      let origin = url.origin != null ? url.origin : `${url.protocol || ""}//${url.hostname || ""}:${port}`;
      let path = url.path != null ? url.path : `${url.pathname || ""}${url.search || ""}`;
      if (origin[origin.length - 1] === "/") {
        origin = origin.slice(0, origin.length - 1);
      }
      if (path && path[0] !== "/") {
        path = `/${path}`;
      }
      return new URL(`${origin}${path}`);
    }
    if (!isHttpOrHttpsPrefixed(url.origin || url.protocol)) {
      throw new InvalidArgumentError("Invalid URL protocol: the URL must start with `http:` or `https:`.");
    }
    return url;
  }
  function parseOrigin(url) {
    url = parseURL(url);
    if (url.pathname !== "/" || url.search || url.hash) {
      throw new InvalidArgumentError("invalid url");
    }
    return url;
  }
  function getHostname(host) {
    if (host[0] === "[") {
      const idx2 = host.indexOf("]");
      assert(idx2 !== -1);
      return host.substring(1, idx2);
    }
    const idx = host.indexOf(":");
    if (idx === -1)
      return host;
    return host.substring(0, idx);
  }
  function getServerName(host) {
    if (!host) {
      return null;
    }
    assert(typeof host === "string");
    const servername = getHostname(host);
    if (net.isIP(servername)) {
      return "";
    }
    return servername;
  }
  function deepClone(obj) {
    return JSON.parse(JSON.stringify(obj));
  }
  function isAsyncIterable(obj) {
    return !!(obj != null && typeof obj[Symbol.asyncIterator] === "function");
  }
  function isIterable(obj) {
    return !!(obj != null && (typeof obj[Symbol.iterator] === "function" || typeof obj[Symbol.asyncIterator] === "function"));
  }
  function bodyLength(body) {
    if (body == null) {
      return 0;
    } else if (isStream(body)) {
      const state = body._readableState;
      return state && state.objectMode === false && state.ended === true && Number.isFinite(state.length) ? state.length : null;
    } else if (isBlobLike(body)) {
      return body.size != null ? body.size : null;
    } else if (isBuffer(body)) {
      return body.byteLength;
    }
    return null;
  }
  function isDestroyed(body) {
    return body && !!(body.destroyed || body[kDestroyed] || stream.isDestroyed?.(body));
  }
  function destroy(stream2, err) {
    if (stream2 == null || !isStream(stream2) || isDestroyed(stream2)) {
      return;
    }
    if (typeof stream2.destroy === "function") {
      if (Object.getPrototypeOf(stream2).constructor === IncomingMessage) {
        stream2.socket = null;
      }
      stream2.destroy(err);
    } else if (err) {
      queueMicrotask(() => {
        stream2.emit("error", err);
      });
    }
    if (stream2.destroyed !== true) {
      stream2[kDestroyed] = true;
    }
  }
  var KEEPALIVE_TIMEOUT_EXPR = /timeout=(\d+)/;
  function parseKeepAliveTimeout(val) {
    const m = val.toString().match(KEEPALIVE_TIMEOUT_EXPR);
    return m ? parseInt(m[1], 10) * 1000 : null;
  }
  function headerNameToString(value) {
    return typeof value === "string" ? headerNameLowerCasedRecord[value] ?? value.toLowerCase() : tree.lookup(value) ?? value.toString("latin1").toLowerCase();
  }
  function bufferToLowerCasedHeaderName(value) {
    return tree.lookup(value) ?? value.toString("latin1").toLowerCase();
  }
  function parseHeaders(headers, obj) {
    if (obj === undefined)
      obj = {};
    for (let i = 0;i < headers.length; i += 2) {
      const key = headerNameToString(headers[i]);
      let val = obj[key];
      if (val) {
        if (typeof val === "string") {
          val = [val];
          obj[key] = val;
        }
        val.push(headers[i + 1].toString("utf8"));
      } else {
        const headersValue = headers[i + 1];
        if (typeof headersValue === "string") {
          obj[key] = headersValue;
        } else {
          obj[key] = Array.isArray(headersValue) ? headersValue.map((x) => x.toString("utf8")) : headersValue.toString("utf8");
        }
      }
    }
    if ("content-length" in obj && "content-disposition" in obj) {
      obj["content-disposition"] = Buffer.from(obj["content-disposition"]).toString("latin1");
    }
    return obj;
  }
  function parseRawHeaders(headers) {
    const len = headers.length;
    const ret = new Array(len);
    let hasContentLength = false;
    let contentDispositionIdx = -1;
    let key;
    let val;
    let kLen = 0;
    for (let n = 0;n < headers.length; n += 2) {
      key = headers[n];
      val = headers[n + 1];
      typeof key !== "string" && (key = key.toString());
      typeof val !== "string" && (val = val.toString("utf8"));
      kLen = key.length;
      if (kLen === 14 && key[7] === "-" && (key === "content-length" || key.toLowerCase() === "content-length")) {
        hasContentLength = true;
      } else if (kLen === 19 && key[7] === "-" && (key === "content-disposition" || key.toLowerCase() === "content-disposition")) {
        contentDispositionIdx = n + 1;
      }
      ret[n] = key;
      ret[n + 1] = val;
    }
    if (hasContentLength && contentDispositionIdx !== -1) {
      ret[contentDispositionIdx] = Buffer.from(ret[contentDispositionIdx]).toString("latin1");
    }
    return ret;
  }
  function isBuffer(buffer) {
    return buffer instanceof Uint8Array || Buffer.isBuffer(buffer);
  }
  function validateHandler(handler, method, upgrade) {
    if (!handler || typeof handler !== "object") {
      throw new InvalidArgumentError("handler must be an object");
    }
    if (typeof handler.onConnect !== "function") {
      throw new InvalidArgumentError("invalid onConnect method");
    }
    if (typeof handler.onError !== "function") {
      throw new InvalidArgumentError("invalid onError method");
    }
    if (typeof handler.onBodySent !== "function" && handler.onBodySent !== undefined) {
      throw new InvalidArgumentError("invalid onBodySent method");
    }
    if (upgrade || method === "CONNECT") {
      if (typeof handler.onUpgrade !== "function") {
        throw new InvalidArgumentError("invalid onUpgrade method");
      }
    } else {
      if (typeof handler.onHeaders !== "function") {
        throw new InvalidArgumentError("invalid onHeaders method");
      }
      if (typeof handler.onData !== "function") {
        throw new InvalidArgumentError("invalid onData method");
      }
      if (typeof handler.onComplete !== "function") {
        throw new InvalidArgumentError("invalid onComplete method");
      }
    }
  }
  function isDisturbed(body) {
    return !!(body && (stream.isDisturbed(body) || body[kBodyUsed]));
  }
  function isErrored(body) {
    return !!(body && stream.isErrored(body));
  }
  function isReadable(body) {
    return !!(body && stream.isReadable(body));
  }
  function getSocketInfo(socket) {
    return {
      localAddress: socket.localAddress,
      localPort: socket.localPort,
      remoteAddress: socket.remoteAddress,
      remotePort: socket.remotePort,
      remoteFamily: socket.remoteFamily,
      timeout: socket.timeout,
      bytesWritten: socket.bytesWritten,
      bytesRead: socket.bytesRead
    };
  }
  function ReadableStreamFrom(iterable) {
    let iterator;
    return new ReadableStream({
      async start() {
        iterator = iterable[Symbol.asyncIterator]();
      },
      async pull(controller) {
        const { done, value } = await iterator.next();
        if (done) {
          queueMicrotask(() => {
            controller.close();
            controller.byobRequest?.respond(0);
          });
        } else {
          const buf = Buffer.isBuffer(value) ? value : Buffer.from(value);
          if (buf.byteLength) {
            controller.enqueue(new Uint8Array(buf));
          }
        }
        return controller.desiredSize > 0;
      },
      async cancel(reason) {
        await iterator.return();
      },
      type: "bytes"
    });
  }
  function isFormDataLike(object) {
    return object && typeof object === "object" && typeof object.append === "function" && typeof object.delete === "function" && typeof object.get === "function" && typeof object.getAll === "function" && typeof object.has === "function" && typeof object.set === "function" && object[Symbol.toStringTag] === "FormData";
  }
  function addAbortListener(signal, listener) {
    if ("addEventListener" in signal) {
      signal.addEventListener("abort", listener, { once: true });
      return () => signal.removeEventListener("abort", listener);
    }
    signal.addListener("abort", listener);
    return () => signal.removeListener("abort", listener);
  }
  var hasToWellFormed = typeof String.prototype.toWellFormed === "function";
  var hasIsWellFormed = typeof String.prototype.isWellFormed === "function";
  function toUSVString(val) {
    return hasToWellFormed ? `${val}`.toWellFormed() : nodeUtil.toUSVString(val);
  }
  function isUSVString(val) {
    return hasIsWellFormed ? `${val}`.isWellFormed() : toUSVString(val) === `${val}`;
  }
  function isTokenCharCode(c) {
    switch (c) {
      case 34:
      case 40:
      case 41:
      case 44:
      case 47:
      case 58:
      case 59:
      case 60:
      case 61:
      case 62:
      case 63:
      case 64:
      case 91:
      case 92:
      case 93:
      case 123:
      case 125:
        return false;
      default:
        return c >= 33 && c <= 126;
    }
  }
  function isValidHTTPToken(characters) {
    if (characters.length === 0) {
      return false;
    }
    for (let i = 0;i < characters.length; ++i) {
      if (!isTokenCharCode(characters.charCodeAt(i))) {
        return false;
      }
    }
    return true;
  }
  var headerCharRegex = /[^\t\x20-\x7e\x80-\xff]/;
  function isValidHeaderValue(characters) {
    return !headerCharRegex.test(characters);
  }
  function parseRangeHeader(range) {
    if (range == null || range === "")
      return { start: 0, end: null, size: null };
    const m = range ? range.match(/^bytes (\d+)-(\d+)\/(\d+)?$/) : null;
    return m ? {
      start: parseInt(m[1]),
      end: m[2] ? parseInt(m[2]) : null,
      size: m[3] ? parseInt(m[3]) : null
    } : null;
  }
  function addListener(obj, name, listener) {
    const listeners = obj[kListeners] ??= [];
    listeners.push([name, listener]);
    obj.on(name, listener);
    return obj;
  }
  function removeAllListeners(obj) {
    for (const [name, listener] of obj[kListeners] ?? []) {
      obj.removeListener(name, listener);
    }
    obj[kListeners] = null;
  }
  function errorRequest(client, request, err) {
    try {
      request.onError(err);
      assert(request.aborted);
    } catch (err2) {
      client.emit("error", err2);
    }
  }
  var kEnumerableProperty = Object.create(null);
  kEnumerableProperty.enumerable = true;
  var normalizedMethodRecordsBase = {
    delete: "DELETE",
    DELETE: "DELETE",
    get: "GET",
    GET: "GET",
    head: "HEAD",
    HEAD: "HEAD",
    options: "OPTIONS",
    OPTIONS: "OPTIONS",
    post: "POST",
    POST: "POST",
    put: "PUT",
    PUT: "PUT"
  };
  var normalizedMethodRecords = {
    ...normalizedMethodRecordsBase,
    patch: "patch",
    PATCH: "PATCH"
  };
  Object.setPrototypeOf(normalizedMethodRecordsBase, null);
  Object.setPrototypeOf(normalizedMethodRecords, null);
  module.exports = {
    kEnumerableProperty,
    nop,
    isDisturbed,
    isErrored,
    isReadable,
    toUSVString,
    isUSVString,
    isBlobLike,
    parseOrigin,
    parseURL,
    getServerName,
    isStream,
    isIterable,
    isAsyncIterable,
    isDestroyed,
    headerNameToString,
    bufferToLowerCasedHeaderName,
    addListener,
    removeAllListeners,
    errorRequest,
    parseRawHeaders,
    parseHeaders,
    parseKeepAliveTimeout,
    destroy,
    bodyLength,
    deepClone,
    ReadableStreamFrom,
    isBuffer,
    validateHandler,
    getSocketInfo,
    isFormDataLike,
    buildURL,
    addAbortListener,
    isValidHTTPToken,
    isValidHeaderValue,
    isTokenCharCode,
    parseRangeHeader,
    normalizedMethodRecordsBase,
    normalizedMethodRecords,
    isValidPort,
    isHttpOrHttpsPrefixed,
    nodeMajor,
    nodeMinor,
    safeHTTPMethods: ["GET", "HEAD", "OPTIONS", "TRACE"],
    wrapRequestBody
  };
});

// node_modules/undici/lib/core/diagnostics.js
var require_diagnostics = __commonJS((exports, module) => {
  var diagnosticsChannel = __require("diagnostics_channel");
  var util = __require("util");
  var undiciDebugLog = util.debuglog("undici");
  var fetchDebuglog = util.debuglog("fetch");
  var websocketDebuglog = util.debuglog("websocket");
  var isClientSet = false;
  var channels = {
    beforeConnect: diagnosticsChannel.channel("undici:client:beforeConnect"),
    connected: diagnosticsChannel.channel("undici:client:connected"),
    connectError: diagnosticsChannel.channel("undici:client:connectError"),
    sendHeaders: diagnosticsChannel.channel("undici:client:sendHeaders"),
    create: diagnosticsChannel.channel("undici:request:create"),
    bodySent: diagnosticsChannel.channel("undici:request:bodySent"),
    headers: diagnosticsChannel.channel("undici:request:headers"),
    trailers: diagnosticsChannel.channel("undici:request:trailers"),
    error: diagnosticsChannel.channel("undici:request:error"),
    open: diagnosticsChannel.channel("undici:websocket:open"),
    close: diagnosticsChannel.channel("undici:websocket:close"),
    socketError: diagnosticsChannel.channel("undici:websocket:socket_error"),
    ping: diagnosticsChannel.channel("undici:websocket:ping"),
    pong: diagnosticsChannel.channel("undici:websocket:pong")
  };
  if (undiciDebugLog.enabled || fetchDebuglog.enabled) {
    const debuglog = fetchDebuglog.enabled ? fetchDebuglog : undiciDebugLog;
    diagnosticsChannel.channel("undici:client:beforeConnect").subscribe((evt) => {
      const {
        connectParams: { version, protocol, port, host }
      } = evt;
      debuglog("connecting to %s using %s%s", `${host}${port ? `:${port}` : ""}`, protocol, version);
    });
    diagnosticsChannel.channel("undici:client:connected").subscribe((evt) => {
      const {
        connectParams: { version, protocol, port, host }
      } = evt;
      debuglog("connected to %s using %s%s", `${host}${port ? `:${port}` : ""}`, protocol, version);
    });
    diagnosticsChannel.channel("undici:client:connectError").subscribe((evt) => {
      const {
        connectParams: { version, protocol, port, host },
        error
      } = evt;
      debuglog("connection to %s using %s%s errored - %s", `${host}${port ? `:${port}` : ""}`, protocol, version, error.message);
    });
    diagnosticsChannel.channel("undici:client:sendHeaders").subscribe((evt) => {
      const {
        request: { method, path, origin }
      } = evt;
      debuglog("sending request to %s %s/%s", method, origin, path);
    });
    diagnosticsChannel.channel("undici:request:headers").subscribe((evt) => {
      const {
        request: { method, path, origin },
        response: { statusCode }
      } = evt;
      debuglog("received response to %s %s/%s - HTTP %d", method, origin, path, statusCode);
    });
    diagnosticsChannel.channel("undici:request:trailers").subscribe((evt) => {
      const {
        request: { method, path, origin }
      } = evt;
      debuglog("trailers received from %s %s/%s", method, origin, path);
    });
    diagnosticsChannel.channel("undici:request:error").subscribe((evt) => {
      const {
        request: { method, path, origin },
        error
      } = evt;
      debuglog("request to %s %s/%s errored - %s", method, origin, path, error.message);
    });
    isClientSet = true;
  }
  if (websocketDebuglog.enabled) {
    if (!isClientSet) {
      const debuglog = undiciDebugLog.enabled ? undiciDebugLog : websocketDebuglog;
      diagnosticsChannel.channel("undici:client:beforeConnect").subscribe((evt) => {
        const {
          connectParams: { version, protocol, port, host }
        } = evt;
        debuglog("connecting to %s%s using %s%s", host, port ? `:${port}` : "", protocol, version);
      });
      diagnosticsChannel.channel("undici:client:connected").subscribe((evt) => {
        const {
          connectParams: { version, protocol, port, host }
        } = evt;
        debuglog("connected to %s%s using %s%s", host, port ? `:${port}` : "", protocol, version);
      });
      diagnosticsChannel.channel("undici:client:connectError").subscribe((evt) => {
        const {
          connectParams: { version, protocol, port, host },
          error
        } = evt;
        debuglog("connection to %s%s using %s%s errored - %s", host, port ? `:${port}` : "", protocol, version, error.message);
      });
      diagnosticsChannel.channel("undici:client:sendHeaders").subscribe((evt) => {
        const {
          request: { method, path, origin }
        } = evt;
        debuglog("sending request to %s %s/%s", method, origin, path);
      });
    }
    diagnosticsChannel.channel("undici:websocket:open").subscribe((evt) => {
      const {
        address: { address, port }
      } = evt;
      websocketDebuglog("connection opened %s%s", address, port ? `:${port}` : "");
    });
    diagnosticsChannel.channel("undici:websocket:close").subscribe((evt) => {
      const { websocket, code, reason } = evt;
      websocketDebuglog("closed connection to %s - %s %s", websocket.url, code, reason);
    });
    diagnosticsChannel.channel("undici:websocket:socket_error").subscribe((err) => {
      websocketDebuglog("connection errored - %s", err.message);
    });
    diagnosticsChannel.channel("undici:websocket:ping").subscribe((evt) => {
      websocketDebuglog("ping received");
    });
    diagnosticsChannel.channel("undici:websocket:pong").subscribe((evt) => {
      websocketDebuglog("pong received");
    });
  }
  module.exports = {
    channels
  };
});

// node_modules/undici/lib/core/request.js
var require_request = __commonJS((exports, module) => {
  var {
    InvalidArgumentError,
    NotSupportedError
  } = require_errors();
  var assert = __require("assert");
  var {
    isValidHTTPToken,
    isValidHeaderValue,
    isStream,
    destroy,
    isBuffer,
    isFormDataLike,
    isIterable,
    isBlobLike,
    buildURL,
    validateHandler,
    getServerName,
    normalizedMethodRecords
  } = require_util();
  var { channels } = require_diagnostics();
  var { headerNameLowerCasedRecord } = require_constants();
  var invalidPathRegex = /[^\u0021-\u00ff]/;
  var kHandler = Symbol("handler");

  class Request2 {
    constructor(origin, {
      path,
      method,
      body,
      headers,
      query,
      idempotent,
      blocking,
      upgrade,
      headersTimeout,
      bodyTimeout,
      reset,
      throwOnError,
      expectContinue,
      servername
    }, handler) {
      if (typeof path !== "string") {
        throw new InvalidArgumentError("path must be a string");
      } else if (path[0] !== "/" && !(path.startsWith("http://") || path.startsWith("https://")) && method !== "CONNECT") {
        throw new InvalidArgumentError("path must be an absolute URL or start with a slash");
      } else if (invalidPathRegex.test(path)) {
        throw new InvalidArgumentError("invalid request path");
      }
      if (typeof method !== "string") {
        throw new InvalidArgumentError("method must be a string");
      } else if (normalizedMethodRecords[method] === undefined && !isValidHTTPToken(method)) {
        throw new InvalidArgumentError("invalid request method");
      }
      if (upgrade && typeof upgrade !== "string") {
        throw new InvalidArgumentError("upgrade must be a string");
      }
      if (upgrade && !isValidHeaderValue(upgrade)) {
        throw new InvalidArgumentError("invalid upgrade header");
      }
      if (headersTimeout != null && (!Number.isFinite(headersTimeout) || headersTimeout < 0)) {
        throw new InvalidArgumentError("invalid headersTimeout");
      }
      if (bodyTimeout != null && (!Number.isFinite(bodyTimeout) || bodyTimeout < 0)) {
        throw new InvalidArgumentError("invalid bodyTimeout");
      }
      if (reset != null && typeof reset !== "boolean") {
        throw new InvalidArgumentError("invalid reset");
      }
      if (expectContinue != null && typeof expectContinue !== "boolean") {
        throw new InvalidArgumentError("invalid expectContinue");
      }
      this.headersTimeout = headersTimeout;
      this.bodyTimeout = bodyTimeout;
      this.throwOnError = throwOnError === true;
      this.method = method;
      this.abort = null;
      if (body == null) {
        this.body = null;
      } else if (isStream(body)) {
        this.body = body;
        const rState = this.body._readableState;
        if (!rState || !rState.autoDestroy) {
          this.endHandler = function autoDestroy() {
            destroy(this);
          };
          this.body.on("end", this.endHandler);
        }
        this.errorHandler = (err) => {
          if (this.abort) {
            this.abort(err);
          } else {
            this.error = err;
          }
        };
        this.body.on("error", this.errorHandler);
      } else if (isBuffer(body)) {
        this.body = body.byteLength ? body : null;
      } else if (ArrayBuffer.isView(body)) {
        this.body = body.buffer.byteLength ? Buffer.from(body.buffer, body.byteOffset, body.byteLength) : null;
      } else if (body instanceof ArrayBuffer) {
        this.body = body.byteLength ? Buffer.from(body) : null;
      } else if (typeof body === "string") {
        this.body = body.length ? Buffer.from(body) : null;
      } else if (isFormDataLike(body) || isIterable(body) || isBlobLike(body)) {
        this.body = body;
      } else {
        throw new InvalidArgumentError("body must be a string, a Buffer, a Readable stream, an iterable, or an async iterable");
      }
      this.completed = false;
      this.aborted = false;
      this.upgrade = upgrade || null;
      this.path = query ? buildURL(path, query) : path;
      this.origin = origin;
      this.idempotent = idempotent == null ? method === "HEAD" || method === "GET" : idempotent;
      this.blocking = blocking == null ? false : blocking;
      this.reset = reset == null ? null : reset;
      this.host = null;
      this.contentLength = null;
      this.contentType = null;
      this.headers = [];
      this.expectContinue = expectContinue != null ? expectContinue : false;
      if (Array.isArray(headers)) {
        if (headers.length % 2 !== 0) {
          throw new InvalidArgumentError("headers array must be even");
        }
        for (let i = 0;i < headers.length; i += 2) {
          processHeader(this, headers[i], headers[i + 1]);
        }
      } else if (headers && typeof headers === "object") {
        if (headers[Symbol.iterator]) {
          for (const header of headers) {
            if (!Array.isArray(header) || header.length !== 2) {
              throw new InvalidArgumentError("headers must be in key-value pair format");
            }
            processHeader(this, header[0], header[1]);
          }
        } else {
          const keys = Object.keys(headers);
          for (let i = 0;i < keys.length; ++i) {
            processHeader(this, keys[i], headers[keys[i]]);
          }
        }
      } else if (headers != null) {
        throw new InvalidArgumentError("headers must be an object or an array");
      }
      validateHandler(handler, method, upgrade);
      this.servername = servername || getServerName(this.host);
      this[kHandler] = handler;
      if (channels.create.hasSubscribers) {
        channels.create.publish({ request: this });
      }
    }
    onBodySent(chunk) {
      if (this[kHandler].onBodySent) {
        try {
          return this[kHandler].onBodySent(chunk);
        } catch (err) {
          this.abort(err);
        }
      }
    }
    onRequestSent() {
      if (channels.bodySent.hasSubscribers) {
        channels.bodySent.publish({ request: this });
      }
      if (this[kHandler].onRequestSent) {
        try {
          return this[kHandler].onRequestSent();
        } catch (err) {
          this.abort(err);
        }
      }
    }
    onConnect(abort) {
      assert(!this.aborted);
      assert(!this.completed);
      if (this.error) {
        abort(this.error);
      } else {
        this.abort = abort;
        return this[kHandler].onConnect(abort);
      }
    }
    onResponseStarted() {
      return this[kHandler].onResponseStarted?.();
    }
    onHeaders(statusCode, headers, resume, statusText) {
      assert(!this.aborted);
      assert(!this.completed);
      if (channels.headers.hasSubscribers) {
        channels.headers.publish({ request: this, response: { statusCode, headers, statusText } });
      }
      try {
        return this[kHandler].onHeaders(statusCode, headers, resume, statusText);
      } catch (err) {
        this.abort(err);
      }
    }
    onData(chunk) {
      assert(!this.aborted);
      assert(!this.completed);
      try {
        return this[kHandler].onData(chunk);
      } catch (err) {
        this.abort(err);
        return false;
      }
    }
    onUpgrade(statusCode, headers, socket) {
      assert(!this.aborted);
      assert(!this.completed);
      return this[kHandler].onUpgrade(statusCode, headers, socket);
    }
    onComplete(trailers) {
      this.onFinally();
      assert(!this.aborted);
      this.completed = true;
      if (channels.trailers.hasSubscribers) {
        channels.trailers.publish({ request: this, trailers });
      }
      try {
        return this[kHandler].onComplete(trailers);
      } catch (err) {
        this.onError(err);
      }
    }
    onError(error) {
      this.onFinally();
      if (channels.error.hasSubscribers) {
        channels.error.publish({ request: this, error });
      }
      if (this.aborted) {
        return;
      }
      this.aborted = true;
      return this[kHandler].onError(error);
    }
    onFinally() {
      if (this.errorHandler) {
        this.body.off("error", this.errorHandler);
        this.errorHandler = null;
      }
      if (this.endHandler) {
        this.body.off("end", this.endHandler);
        this.endHandler = null;
      }
    }
    addHeader(key, value) {
      processHeader(this, key, value);
      return this;
    }
  }
  function processHeader(request, key, val) {
    if (val && (typeof val === "object" && !Array.isArray(val))) {
      throw new InvalidArgumentError(`invalid ${key} header`);
    } else if (val === undefined) {
      return;
    }
    let headerName = headerNameLowerCasedRecord[key];
    if (headerName === undefined) {
      headerName = key.toLowerCase();
      if (headerNameLowerCasedRecord[headerName] === undefined && !isValidHTTPToken(headerName)) {
        throw new InvalidArgumentError("invalid header key");
      }
    }
    if (Array.isArray(val)) {
      const arr = [];
      for (let i = 0;i < val.length; i++) {
        if (typeof val[i] === "string") {
          if (!isValidHeaderValue(val[i])) {
            throw new InvalidArgumentError(`invalid ${key} header`);
          }
          arr.push(val[i]);
        } else if (val[i] === null) {
          arr.push("");
        } else if (typeof val[i] === "object") {
          throw new InvalidArgumentError(`invalid ${key} header`);
        } else {
          arr.push(`${val[i]}`);
        }
      }
      val = arr;
    } else if (typeof val === "string") {
      if (!isValidHeaderValue(val)) {
        throw new InvalidArgumentError(`invalid ${key} header`);
      }
    } else if (val === null) {
      val = "";
    } else {
      val = `${val}`;
    }
    if (headerName === "host") {
      if (request.host !== null) {
        throw new InvalidArgumentError("duplicate host header");
      }
      if (typeof val !== "string") {
        throw new InvalidArgumentError("invalid host header");
      }
      request.host = val;
    } else if (headerName === "content-length") {
      if (request.contentLength !== null) {
        throw new InvalidArgumentError("duplicate content-length header");
      }
      request.contentLength = parseInt(val, 10);
      if (!Number.isFinite(request.contentLength)) {
        throw new InvalidArgumentError("invalid content-length header");
      }
    } else if (request.contentType === null && headerName === "content-type") {
      request.contentType = val;
      request.headers.push(key, val);
    } else if (headerName === "transfer-encoding" || headerName === "keep-alive" || headerName === "upgrade") {
      throw new InvalidArgumentError(`invalid ${headerName} header`);
    } else if (headerName === "connection") {
      const value = typeof val === "string" ? val.toLowerCase() : null;
      if (value !== "close" && value !== "keep-alive") {
        throw new InvalidArgumentError("invalid connection header");
      }
      if (value === "close") {
        request.reset = true;
      }
    } else if (headerName === "expect") {
      throw new NotSupportedError("expect header not supported");
    } else {
      request.headers.push(key, val);
    }
  }
  module.exports = Request2;
});

// node_modules/undici/lib/dispatcher/dispatcher.js
var require_dispatcher = __commonJS((exports, module) => {
  var EventEmitter = __require("events");

  class Dispatcher extends EventEmitter {
    dispatch() {
      throw new Error("not implemented");
    }
    close() {
      throw new Error("not implemented");
    }
    destroy() {
      throw new Error("not implemented");
    }
    compose(...args) {
      const interceptors = Array.isArray(args[0]) ? args[0] : args;
      let dispatch = this.dispatch.bind(this);
      for (const interceptor of interceptors) {
        if (interceptor == null) {
          continue;
        }
        if (typeof interceptor !== "function") {
          throw new TypeError(`invalid interceptor, expected function received ${typeof interceptor}`);
        }
        dispatch = interceptor(dispatch);
        if (dispatch == null || typeof dispatch !== "function" || dispatch.length !== 2) {
          throw new TypeError("invalid interceptor");
        }
      }
      return new ComposedDispatcher(this, dispatch);
    }
  }

  class ComposedDispatcher extends Dispatcher {
    #dispatcher = null;
    #dispatch = null;
    constructor(dispatcher, dispatch) {
      super();
      this.#dispatcher = dispatcher;
      this.#dispatch = dispatch;
    }
    dispatch(...args) {
      this.#dispatch(...args);
    }
    close(...args) {
      return this.#dispatcher.close(...args);
    }
    destroy(...args) {
      return this.#dispatcher.destroy(...args);
    }
  }
  module.exports = Dispatcher;
});

// node_modules/undici/lib/dispatcher/dispatcher-base.js
var require_dispatcher_base = __commonJS((exports, module) => {
  var Dispatcher = require_dispatcher();
  var {
    ClientDestroyedError,
    ClientClosedError,
    InvalidArgumentError
  } = require_errors();
  var { kDestroy, kClose, kClosed, kDestroyed, kDispatch, kInterceptors } = require_symbols();
  var kOnDestroyed = Symbol("onDestroyed");
  var kOnClosed = Symbol("onClosed");
  var kInterceptedDispatch = Symbol("Intercepted Dispatch");

  class DispatcherBase extends Dispatcher {
    constructor() {
      super();
      this[kDestroyed] = false;
      this[kOnDestroyed] = null;
      this[kClosed] = false;
      this[kOnClosed] = [];
    }
    get destroyed() {
      return this[kDestroyed];
    }
    get closed() {
      return this[kClosed];
    }
    get interceptors() {
      return this[kInterceptors];
    }
    set interceptors(newInterceptors) {
      if (newInterceptors) {
        for (let i = newInterceptors.length - 1;i >= 0; i--) {
          const interceptor = this[kInterceptors][i];
          if (typeof interceptor !== "function") {
            throw new InvalidArgumentError("interceptor must be an function");
          }
        }
      }
      this[kInterceptors] = newInterceptors;
    }
    close(callback) {
      if (callback === undefined) {
        return new Promise((resolve3, reject) => {
          this.close((err, data) => {
            return err ? reject(err) : resolve3(data);
          });
        });
      }
      if (typeof callback !== "function") {
        throw new InvalidArgumentError("invalid callback");
      }
      if (this[kDestroyed]) {
        queueMicrotask(() => callback(new ClientDestroyedError, null));
        return;
      }
      if (this[kClosed]) {
        if (this[kOnClosed]) {
          this[kOnClosed].push(callback);
        } else {
          queueMicrotask(() => callback(null, null));
        }
        return;
      }
      this[kClosed] = true;
      this[kOnClosed].push(callback);
      const onClosed = () => {
        const callbacks = this[kOnClosed];
        this[kOnClosed] = null;
        for (let i = 0;i < callbacks.length; i++) {
          callbacks[i](null, null);
        }
      };
      this[kClose]().then(() => this.destroy()).then(() => {
        queueMicrotask(onClosed);
      });
    }
    destroy(err, callback) {
      if (typeof err === "function") {
        callback = err;
        err = null;
      }
      if (callback === undefined) {
        return new Promise((resolve3, reject) => {
          this.destroy(err, (err2, data) => {
            return err2 ? reject(err2) : resolve3(data);
          });
        });
      }
      if (typeof callback !== "function") {
        throw new InvalidArgumentError("invalid callback");
      }
      if (this[kDestroyed]) {
        if (this[kOnDestroyed]) {
          this[kOnDestroyed].push(callback);
        } else {
          queueMicrotask(() => callback(null, null));
        }
        return;
      }
      if (!err) {
        err = new ClientDestroyedError;
      }
      this[kDestroyed] = true;
      this[kOnDestroyed] = this[kOnDestroyed] || [];
      this[kOnDestroyed].push(callback);
      const onDestroyed = () => {
        const callbacks = this[kOnDestroyed];
        this[kOnDestroyed] = null;
        for (let i = 0;i < callbacks.length; i++) {
          callbacks[i](null, null);
        }
      };
      this[kDestroy](err).then(() => {
        queueMicrotask(onDestroyed);
      });
    }
    [kInterceptedDispatch](opts, handler) {
      if (!this[kInterceptors] || this[kInterceptors].length === 0) {
        this[kInterceptedDispatch] = this[kDispatch];
        return this[kDispatch](opts, handler);
      }
      let dispatch = this[kDispatch].bind(this);
      for (let i = this[kInterceptors].length - 1;i >= 0; i--) {
        dispatch = this[kInterceptors][i](dispatch);
      }
      this[kInterceptedDispatch] = dispatch;
      return dispatch(opts, handler);
    }
    dispatch(opts, handler) {
      if (!handler || typeof handler !== "object") {
        throw new InvalidArgumentError("handler must be an object");
      }
      try {
        if (!opts || typeof opts !== "object") {
          throw new InvalidArgumentError("opts must be an object.");
        }
        if (this[kDestroyed] || this[kOnDestroyed]) {
          throw new ClientDestroyedError;
        }
        if (this[kClosed]) {
          throw new ClientClosedError;
        }
        return this[kInterceptedDispatch](opts, handler);
      } catch (err) {
        if (typeof handler.onError !== "function") {
          throw new InvalidArgumentError("invalid onError method");
        }
        handler.onError(err);
        return false;
      }
    }
  }
  module.exports = DispatcherBase;
});

// node_modules/undici/lib/util/timers.js
var require_timers = __commonJS((exports, module) => {
  var fastNow = 0;
  var RESOLUTION_MS = 1000;
  var TICK_MS = (RESOLUTION_MS >> 1) - 1;
  var fastNowTimeout;
  var kFastTimer = Symbol("kFastTimer");
  var fastTimers = [];
  var NOT_IN_LIST = -2;
  var TO_BE_CLEARED = -1;
  var PENDING = 0;
  var ACTIVE = 1;
  function onTick() {
    fastNow += TICK_MS;
    let idx = 0;
    let len = fastTimers.length;
    while (idx < len) {
      const timer = fastTimers[idx];
      if (timer._state === PENDING) {
        timer._idleStart = fastNow - TICK_MS;
        timer._state = ACTIVE;
      } else if (timer._state === ACTIVE && fastNow >= timer._idleStart + timer._idleTimeout) {
        timer._state = TO_BE_CLEARED;
        timer._idleStart = -1;
        timer._onTimeout(timer._timerArg);
      }
      if (timer._state === TO_BE_CLEARED) {
        timer._state = NOT_IN_LIST;
        if (--len !== 0) {
          fastTimers[idx] = fastTimers[len];
        }
      } else {
        ++idx;
      }
    }
    fastTimers.length = len;
    if (fastTimers.length !== 0) {
      refreshTimeout();
    }
  }
  function refreshTimeout() {
    if (fastNowTimeout) {
      fastNowTimeout.refresh();
    } else {
      clearTimeout(fastNowTimeout);
      fastNowTimeout = setTimeout(onTick, TICK_MS);
      if (fastNowTimeout.unref) {
        fastNowTimeout.unref();
      }
    }
  }

  class FastTimer {
    [kFastTimer] = true;
    _state = NOT_IN_LIST;
    _idleTimeout = -1;
    _idleStart = -1;
    _onTimeout;
    _timerArg;
    constructor(callback, delay, arg) {
      this._onTimeout = callback;
      this._idleTimeout = delay;
      this._timerArg = arg;
      this.refresh();
    }
    refresh() {
      if (this._state === NOT_IN_LIST) {
        fastTimers.push(this);
      }
      if (!fastNowTimeout || fastTimers.length === 1) {
        refreshTimeout();
      }
      this._state = PENDING;
    }
    clear() {
      this._state = TO_BE_CLEARED;
      this._idleStart = -1;
    }
  }
  module.exports = {
    setTimeout(callback, delay, arg) {
      return delay <= RESOLUTION_MS ? setTimeout(callback, delay, arg) : new FastTimer(callback, delay, arg);
    },
    clearTimeout(timeout) {
      if (timeout[kFastTimer]) {
        timeout.clear();
      } else {
        clearTimeout(timeout);
      }
    },
    setFastTimeout(callback, delay, arg) {
      return new FastTimer(callback, delay, arg);
    },
    clearFastTimeout(timeout) {
      timeout.clear();
    },
    now() {
      return fastNow;
    },
    tick(delay = 0) {
      fastNow += delay - RESOLUTION_MS + 1;
      onTick();
      onTick();
    },
    reset() {
      fastNow = 0;
      fastTimers.length = 0;
      clearTimeout(fastNowTimeout);
      fastNowTimeout = null;
    },
    kFastTimer
  };
});

// node_modules/undici/lib/core/connect.js
var require_connect = __commonJS((exports, module) => {
  var net = __require("net");
  var assert = __require("assert");
  var util = require_util();
  var { InvalidArgumentError, ConnectTimeoutError } = require_errors();
  var timers = require_timers();
  function noop() {}
  var tls;
  var SessionCache;
  if (global.FinalizationRegistry && !(process.env.NODE_V8_COVERAGE || process.env.UNDICI_NO_FG)) {
    SessionCache = class WeakSessionCache {
      constructor(maxCachedSessions) {
        this._maxCachedSessions = maxCachedSessions;
        this._sessionCache = new Map;
        this._sessionRegistry = new global.FinalizationRegistry((key) => {
          if (this._sessionCache.size < this._maxCachedSessions) {
            return;
          }
          const ref = this._sessionCache.get(key);
          if (ref !== undefined && ref.deref() === undefined) {
            this._sessionCache.delete(key);
          }
        });
      }
      get(sessionKey) {
        const ref = this._sessionCache.get(sessionKey);
        return ref ? ref.deref() : null;
      }
      set(sessionKey, session) {
        if (this._maxCachedSessions === 0) {
          return;
        }
        this._sessionCache.set(sessionKey, new WeakRef(session));
        this._sessionRegistry.register(session, sessionKey);
      }
    };
  } else {
    SessionCache = class SimpleSessionCache {
      constructor(maxCachedSessions) {
        this._maxCachedSessions = maxCachedSessions;
        this._sessionCache = new Map;
      }
      get(sessionKey) {
        return this._sessionCache.get(sessionKey);
      }
      set(sessionKey, session) {
        if (this._maxCachedSessions === 0) {
          return;
        }
        if (this._sessionCache.size >= this._maxCachedSessions) {
          const { value: oldestKey } = this._sessionCache.keys().next();
          this._sessionCache.delete(oldestKey);
        }
        this._sessionCache.set(sessionKey, session);
      }
    };
  }
  function buildConnector({ allowH2, maxCachedSessions, socketPath, timeout, session: customSession, ...opts }) {
    if (maxCachedSessions != null && (!Number.isInteger(maxCachedSessions) || maxCachedSessions < 0)) {
      throw new InvalidArgumentError("maxCachedSessions must be a positive integer or zero");
    }
    const options = { path: socketPath, ...opts };
    const sessionCache = new SessionCache(maxCachedSessions == null ? 100 : maxCachedSessions);
    timeout = timeout == null ? 1e4 : timeout;
    allowH2 = allowH2 != null ? allowH2 : false;
    return function connect({ hostname, host, protocol, port, servername, localAddress, httpSocket }, callback) {
      let socket;
      if (protocol === "https:") {
        if (!tls) {
          tls = __require("tls");
        }
        servername = servername || options.servername || util.getServerName(host) || null;
        const sessionKey = servername || hostname;
        assert(sessionKey);
        const session = customSession || sessionCache.get(sessionKey) || null;
        port = port || 443;
        socket = tls.connect({
          highWaterMark: 16384,
          ...options,
          servername,
          session,
          localAddress,
          ALPNProtocols: allowH2 ? ["http/1.1", "h2"] : ["http/1.1"],
          socket: httpSocket,
          port,
          host: hostname
        });
        socket.on("session", function(session2) {
          sessionCache.set(sessionKey, session2);
        });
      } else {
        assert(!httpSocket, "httpSocket can only be sent on TLS update");
        port = port || 80;
        socket = net.connect({
          highWaterMark: 64 * 1024,
          ...options,
          localAddress,
          port,
          host: hostname
        });
      }
      if (options.keepAlive == null || options.keepAlive) {
        const keepAliveInitialDelay = options.keepAliveInitialDelay === undefined ? 60000 : options.keepAliveInitialDelay;
        socket.setKeepAlive(true, keepAliveInitialDelay);
      }
      const clearConnectTimeout = setupConnectTimeout(new WeakRef(socket), { timeout, hostname, port });
      socket.setNoDelay(true).once(protocol === "https:" ? "secureConnect" : "connect", function() {
        queueMicrotask(clearConnectTimeout);
        if (callback) {
          const cb = callback;
          callback = null;
          cb(null, this);
        }
      }).on("error", function(err) {
        queueMicrotask(clearConnectTimeout);
        if (callback) {
          const cb = callback;
          callback = null;
          cb(err);
        }
      });
      return socket;
    };
  }
  var setupConnectTimeout = process.platform === "win32" ? (socketWeakRef, opts) => {
    if (!opts.timeout) {
      return noop;
    }
    let s1 = null;
    let s2 = null;
    const fastTimer = timers.setFastTimeout(() => {
      s1 = setImmediate(() => {
        s2 = setImmediate(() => onConnectTimeout(socketWeakRef.deref(), opts));
      });
    }, opts.timeout);
    return () => {
      timers.clearFastTimeout(fastTimer);
      clearImmediate(s1);
      clearImmediate(s2);
    };
  } : (socketWeakRef, opts) => {
    if (!opts.timeout) {
      return noop;
    }
    let s1 = null;
    const fastTimer = timers.setFastTimeout(() => {
      s1 = setImmediate(() => {
        onConnectTimeout(socketWeakRef.deref(), opts);
      });
    }, opts.timeout);
    return () => {
      timers.clearFastTimeout(fastTimer);
      clearImmediate(s1);
    };
  };
  function onConnectTimeout(socket, opts) {
    if (socket == null) {
      return;
    }
    let message = "Connect Timeout Error";
    if (Array.isArray(socket.autoSelectFamilyAttemptedAddresses)) {
      message += ` (attempted addresses: ${socket.autoSelectFamilyAttemptedAddresses.join(", ")},`;
    } else {
      message += ` (attempted address: ${opts.hostname}:${opts.port},`;
    }
    message += ` timeout: ${opts.timeout}ms)`;
    util.destroy(socket, new ConnectTimeoutError(message));
  }
  module.exports = buildConnector;
});

// node_modules/undici/lib/llhttp/utils.js
var require_utils = __commonJS((exports) => {
  Object.defineProperty(exports, "__esModule", { value: true });
  exports.enumToMap = undefined;
  function enumToMap(obj) {
    const res = {};
    Object.keys(obj).forEach((key) => {
      const value = obj[key];
      if (typeof value === "number") {
        res[key] = value;
      }
    });
    return res;
  }
  exports.enumToMap = enumToMap;
});

// node_modules/undici/lib/llhttp/constants.js
var require_constants2 = __commonJS((exports) => {
  Object.defineProperty(exports, "__esModule", { value: true });
  exports.SPECIAL_HEADERS = exports.HEADER_STATE = exports.MINOR = exports.MAJOR = exports.CONNECTION_TOKEN_CHARS = exports.HEADER_CHARS = exports.TOKEN = exports.STRICT_TOKEN = exports.HEX = exports.URL_CHAR = exports.STRICT_URL_CHAR = exports.USERINFO_CHARS = exports.MARK = exports.ALPHANUM = exports.NUM = exports.HEX_MAP = exports.NUM_MAP = exports.ALPHA = exports.FINISH = exports.H_METHOD_MAP = exports.METHOD_MAP = exports.METHODS_RTSP = exports.METHODS_ICE = exports.METHODS_HTTP = exports.METHODS = exports.LENIENT_FLAGS = exports.FLAGS = exports.TYPE = exports.ERROR = undefined;
  var utils_1 = require_utils();
  var ERROR;
  (function(ERROR2) {
    ERROR2[ERROR2["OK"] = 0] = "OK";
    ERROR2[ERROR2["INTERNAL"] = 1] = "INTERNAL";
    ERROR2[ERROR2["STRICT"] = 2] = "STRICT";
    ERROR2[ERROR2["LF_EXPECTED"] = 3] = "LF_EXPECTED";
    ERROR2[ERROR2["UNEXPECTED_CONTENT_LENGTH"] = 4] = "UNEXPECTED_CONTENT_LENGTH";
    ERROR2[ERROR2["CLOSED_CONNECTION"] = 5] = "CLOSED_CONNECTION";
    ERROR2[ERROR2["INVALID_METHOD"] = 6] = "INVALID_METHOD";
    ERROR2[ERROR2["INVALID_URL"] = 7] = "INVALID_URL";
    ERROR2[ERROR2["INVALID_CONSTANT"] = 8] = "INVALID_CONSTANT";
    ERROR2[ERROR2["INVALID_VERSION"] = 9] = "INVALID_VERSION";
    ERROR2[ERROR2["INVALID_HEADER_TOKEN"] = 10] = "INVALID_HEADER_TOKEN";
    ERROR2[ERROR2["INVALID_CONTENT_LENGTH"] = 11] = "INVALID_CONTENT_LENGTH";
    ERROR2[ERROR2["INVALID_CHUNK_SIZE"] = 12] = "INVALID_CHUNK_SIZE";
    ERROR2[ERROR2["INVALID_STATUS"] = 13] = "INVALID_STATUS";
    ERROR2[ERROR2["INVALID_EOF_STATE"] = 14] = "INVALID_EOF_STATE";
    ERROR2[ERROR2["INVALID_TRANSFER_ENCODING"] = 15] = "INVALID_TRANSFER_ENCODING";
    ERROR2[ERROR2["CB_MESSAGE_BEGIN"] = 16] = "CB_MESSAGE_BEGIN";
    ERROR2[ERROR2["CB_HEADERS_COMPLETE"] = 17] = "CB_HEADERS_COMPLETE";
    ERROR2[ERROR2["CB_MESSAGE_COMPLETE"] = 18] = "CB_MESSAGE_COMPLETE";
    ERROR2[ERROR2["CB_CHUNK_HEADER"] = 19] = "CB_CHUNK_HEADER";
    ERROR2[ERROR2["CB_CHUNK_COMPLETE"] = 20] = "CB_CHUNK_COMPLETE";
    ERROR2[ERROR2["PAUSED"] = 21] = "PAUSED";
    ERROR2[ERROR2["PAUSED_UPGRADE"] = 22] = "PAUSED_UPGRADE";
    ERROR2[ERROR2["PAUSED_H2_UPGRADE"] = 23] = "PAUSED_H2_UPGRADE";
    ERROR2[ERROR2["USER"] = 24] = "USER";
  })(ERROR = exports.ERROR || (exports.ERROR = {}));
  var TYPE;
  (function(TYPE2) {
    TYPE2[TYPE2["BOTH"] = 0] = "BOTH";
    TYPE2[TYPE2["REQUEST"] = 1] = "REQUEST";
    TYPE2[TYPE2["RESPONSE"] = 2] = "RESPONSE";
  })(TYPE = exports.TYPE || (exports.TYPE = {}));
  var FLAGS;
  (function(FLAGS2) {
    FLAGS2[FLAGS2["CONNECTION_KEEP_ALIVE"] = 1] = "CONNECTION_KEEP_ALIVE";
    FLAGS2[FLAGS2["CONNECTION_CLOSE"] = 2] = "CONNECTION_CLOSE";
    FLAGS2[FLAGS2["CONNECTION_UPGRADE"] = 4] = "CONNECTION_UPGRADE";
    FLAGS2[FLAGS2["CHUNKED"] = 8] = "CHUNKED";
    FLAGS2[FLAGS2["UPGRADE"] = 16] = "UPGRADE";
    FLAGS2[FLAGS2["CONTENT_LENGTH"] = 32] = "CONTENT_LENGTH";
    FLAGS2[FLAGS2["SKIPBODY"] = 64] = "SKIPBODY";
    FLAGS2[FLAGS2["TRAILING"] = 128] = "TRAILING";
    FLAGS2[FLAGS2["TRANSFER_ENCODING"] = 512] = "TRANSFER_ENCODING";
  })(FLAGS = exports.FLAGS || (exports.FLAGS = {}));
  var LENIENT_FLAGS;
  (function(LENIENT_FLAGS2) {
    LENIENT_FLAGS2[LENIENT_FLAGS2["HEADERS"] = 1] = "HEADERS";
    LENIENT_FLAGS2[LENIENT_FLAGS2["CHUNKED_LENGTH"] = 2] = "CHUNKED_LENGTH";
    LENIENT_FLAGS2[LENIENT_FLAGS2["KEEP_ALIVE"] = 4] = "KEEP_ALIVE";
  })(LENIENT_FLAGS = exports.LENIENT_FLAGS || (exports.LENIENT_FLAGS = {}));
  var METHODS;
  (function(METHODS2) {
    METHODS2[METHODS2["DELETE"] = 0] = "DELETE";
    METHODS2[METHODS2["GET"] = 1] = "GET";
    METHODS2[METHODS2["HEAD"] = 2] = "HEAD";
    METHODS2[METHODS2["POST"] = 3] = "POST";
    METHODS2[METHODS2["PUT"] = 4] = "PUT";
    METHODS2[METHODS2["CONNECT"] = 5] = "CONNECT";
    METHODS2[METHODS2["OPTIONS"] = 6] = "OPTIONS";
    METHODS2[METHODS2["TRACE"] = 7] = "TRACE";
    METHODS2[METHODS2["COPY"] = 8] = "COPY";
    METHODS2[METHODS2["LOCK"] = 9] = "LOCK";
    METHODS2[METHODS2["MKCOL"] = 10] = "MKCOL";
    METHODS2[METHODS2["MOVE"] = 11] = "MOVE";
    METHODS2[METHODS2["PROPFIND"] = 12] = "PROPFIND";
    METHODS2[METHODS2["PROPPATCH"] = 13] = "PROPPATCH";
    METHODS2[METHODS2["SEARCH"] = 14] = "SEARCH";
    METHODS2[METHODS2["UNLOCK"] = 15] = "UNLOCK";
    METHODS2[METHODS2["BIND"] = 16] = "BIND";
    METHODS2[METHODS2["REBIND"] = 17] = "REBIND";
    METHODS2[METHODS2["UNBIND"] = 18] = "UNBIND";
    METHODS2[METHODS2["ACL"] = 19] = "ACL";
    METHODS2[METHODS2["REPORT"] = 20] = "REPORT";
    METHODS2[METHODS2["MKACTIVITY"] = 21] = "MKACTIVITY";
    METHODS2[METHODS2["CHECKOUT"] = 22] = "CHECKOUT";
    METHODS2[METHODS2["MERGE"] = 23] = "MERGE";
    METHODS2[METHODS2["M-SEARCH"] = 24] = "M-SEARCH";
    METHODS2[METHODS2["NOTIFY"] = 25] = "NOTIFY";
    METHODS2[METHODS2["SUBSCRIBE"] = 26] = "SUBSCRIBE";
    METHODS2[METHODS2["UNSUBSCRIBE"] = 27] = "UNSUBSCRIBE";
    METHODS2[METHODS2["PATCH"] = 28] = "PATCH";
    METHODS2[METHODS2["PURGE"] = 29] = "PURGE";
    METHODS2[METHODS2["MKCALENDAR"] = 30] = "MKCALENDAR";
    METHODS2[METHODS2["LINK"] = 31] = "LINK";
    METHODS2[METHODS2["UNLINK"] = 32] = "UNLINK";
    METHODS2[METHODS2["SOURCE"] = 33] = "SOURCE";
    METHODS2[METHODS2["PRI"] = 34] = "PRI";
    METHODS2[METHODS2["DESCRIBE"] = 35] = "DESCRIBE";
    METHODS2[METHODS2["ANNOUNCE"] = 36] = "ANNOUNCE";
    METHODS2[METHODS2["SETUP"] = 37] = "SETUP";
    METHODS2[METHODS2["PLAY"] = 38] = "PLAY";
    METHODS2[METHODS2["PAUSE"] = 39] = "PAUSE";
    METHODS2[METHODS2["TEARDOWN"] = 40] = "TEARDOWN";
    METHODS2[METHODS2["GET_PARAMETER"] = 41] = "GET_PARAMETER";
    METHODS2[METHODS2["SET_PARAMETER"] = 42] = "SET_PARAMETER";
    METHODS2[METHODS2["REDIRECT"] = 43] = "REDIRECT";
    METHODS2[METHODS2["RECORD"] = 44] = "RECORD";
    METHODS2[METHODS2["FLUSH"] = 45] = "FLUSH";
  })(METHODS = exports.METHODS || (exports.METHODS = {}));
  exports.METHODS_HTTP = [
    METHODS.DELETE,
    METHODS.GET,
    METHODS.HEAD,
    METHODS.POST,
    METHODS.PUT,
    METHODS.CONNECT,
    METHODS.OPTIONS,
    METHODS.TRACE,
    METHODS.COPY,
    METHODS.LOCK,
    METHODS.MKCOL,
    METHODS.MOVE,
    METHODS.PROPFIND,
    METHODS.PROPPATCH,
    METHODS.SEARCH,
    METHODS.UNLOCK,
    METHODS.BIND,
    METHODS.REBIND,
    METHODS.UNBIND,
    METHODS.ACL,
    METHODS.REPORT,
    METHODS.MKACTIVITY,
    METHODS.CHECKOUT,
    METHODS.MERGE,
    METHODS["M-SEARCH"],
    METHODS.NOTIFY,
    METHODS.SUBSCRIBE,
    METHODS.UNSUBSCRIBE,
    METHODS.PATCH,
    METHODS.PURGE,
    METHODS.MKCALENDAR,
    METHODS.LINK,
    METHODS.UNLINK,
    METHODS.PRI,
    METHODS.SOURCE
  ];
  exports.METHODS_ICE = [
    METHODS.SOURCE
  ];
  exports.METHODS_RTSP = [
    METHODS.OPTIONS,
    METHODS.DESCRIBE,
    METHODS.ANNOUNCE,
    METHODS.SETUP,
    METHODS.PLAY,
    METHODS.PAUSE,
    METHODS.TEARDOWN,
    METHODS.GET_PARAMETER,
    METHODS.SET_PARAMETER,
    METHODS.REDIRECT,
    METHODS.RECORD,
    METHODS.FLUSH,
    METHODS.GET,
    METHODS.POST
  ];
  exports.METHOD_MAP = utils_1.enumToMap(METHODS);
  exports.H_METHOD_MAP = {};
  Object.keys(exports.METHOD_MAP).forEach((key) => {
    if (/^H/.test(key)) {
      exports.H_METHOD_MAP[key] = exports.METHOD_MAP[key];
    }
  });
  var FINISH;
  (function(FINISH2) {
    FINISH2[FINISH2["SAFE"] = 0] = "SAFE";
    FINISH2[FINISH2["SAFE_WITH_CB"] = 1] = "SAFE_WITH_CB";
    FINISH2[FINISH2["UNSAFE"] = 2] = "UNSAFE";
  })(FINISH = exports.FINISH || (exports.FINISH = {}));
  exports.ALPHA = [];
  for (let i = 65;i <= 90; i++) {
    exports.ALPHA.push(String.fromCharCode(i));
    exports.ALPHA.push(String.fromCharCode(i + 32));
  }
  exports.NUM_MAP = {
    0: 0,
    1: 1,
    2: 2,
    3: 3,
    4: 4,
    5: 5,
    6: 6,
    7: 7,
    8: 8,
    9: 9
  };
  exports.HEX_MAP = {
    0: 0,
    1: 1,
    2: 2,
    3: 3,
    4: 4,
    5: 5,
    6: 6,
    7: 7,
    8: 8,
    9: 9,
    A: 10,
    B: 11,
    C: 12,
    D: 13,
    E: 14,
    F: 15,
    a: 10,
    b: 11,
    c: 12,
    d: 13,
    e: 14,
    f: 15
  };
  exports.NUM = [
    "0",
    "1",
    "2",
    "3",
    "4",
    "5",
    "6",
    "7",
    "8",
    "9"
  ];
  exports.ALPHANUM = exports.ALPHA.concat(exports.NUM);
  exports.MARK = ["-", "_", ".", "!", "~", "*", "'", "(", ")"];
  exports.USERINFO_CHARS = exports.ALPHANUM.concat(exports.MARK).concat(["%", ";", ":", "&", "=", "+", "$", ","]);
  exports.STRICT_URL_CHAR = [
    "!",
    '"',
    "$",
    "%",
    "&",
    "'",
    "(",
    ")",
    "*",
    "+",
    ",",
    "-",
    ".",
    "/",
    ":",
    ";",
    "<",
    "=",
    ">",
    "@",
    "[",
    "\\",
    "]",
    "^",
    "_",
    "`",
    "{",
    "|",
    "}",
    "~"
  ].concat(exports.ALPHANUM);
  exports.URL_CHAR = exports.STRICT_URL_CHAR.concat(["\t", "\f"]);
  for (let i = 128;i <= 255; i++) {
    exports.URL_CHAR.push(i);
  }
  exports.HEX = exports.NUM.concat(["a", "b", "c", "d", "e", "f", "A", "B", "C", "D", "E", "F"]);
  exports.STRICT_TOKEN = [
    "!",
    "#",
    "$",
    "%",
    "&",
    "'",
    "*",
    "+",
    "-",
    ".",
    "^",
    "_",
    "`",
    "|",
    "~"
  ].concat(exports.ALPHANUM);
  exports.TOKEN = exports.STRICT_TOKEN.concat([" "]);
  exports.HEADER_CHARS = ["\t"];
  for (let i = 32;i <= 255; i++) {
    if (i !== 127) {
      exports.HEADER_CHARS.push(i);
    }
  }
  exports.CONNECTION_TOKEN_CHARS = exports.HEADER_CHARS.filter((c) => c !== 44);
  exports.MAJOR = exports.NUM_MAP;
  exports.MINOR = exports.MAJOR;
  var HEADER_STATE;
  (function(HEADER_STATE2) {
    HEADER_STATE2[HEADER_STATE2["GENERAL"] = 0] = "GENERAL";
    HEADER_STATE2[HEADER_STATE2["CONNECTION"] = 1] = "CONNECTION";
    HEADER_STATE2[HEADER_STATE2["CONTENT_LENGTH"] = 2] = "CONTENT_LENGTH";
    HEADER_STATE2[HEADER_STATE2["TRANSFER_ENCODING"] = 3] = "TRANSFER_ENCODING";
    HEADER_STATE2[HEADER_STATE2["UPGRADE"] = 4] = "UPGRADE";
    HEADER_STATE2[HEADER_STATE2["CONNECTION_KEEP_ALIVE"] = 5] = "CONNECTION_KEEP_ALIVE";
    HEADER_STATE2[HEADER_STATE2["CONNECTION_CLOSE"] = 6] = "CONNECTION_CLOSE";
    HEADER_STATE2[HEADER_STATE2["CONNECTION_UPGRADE"] = 7] = "CONNECTION_UPGRADE";
    HEADER_STATE2[HEADER_STATE2["TRANSFER_ENCODING_CHUNKED"] = 8] = "TRANSFER_ENCODING_CHUNKED";
  })(HEADER_STATE = exports.HEADER_STATE || (exports.HEADER_STATE = {}));
  exports.SPECIAL_HEADERS = {
    connection: HEADER_STATE.CONNECTION,
    "content-length": HEADER_STATE.CONTENT_LENGTH,
    "proxy-connection": HEADER_STATE.CONNECTION,
    "transfer-encoding": HEADER_STATE.TRANSFER_ENCODING,
    upgrade: HEADER_STATE.UPGRADE
  };
});

// node_modules/undici/lib/llhttp/llhttp-wasm.js
var require_llhttp_wasm = __commonJS((exports, module) => {
  var { Buffer: Buffer2 } = __require("buffer");
  module.exports = Buffer2.from("AGFzbQEAAAABJwdgAX8Bf2ADf39/AX9gAX8AYAJ/fwBgBH9/f38Bf2AAAGADf39/AALLAQgDZW52GHdhc21fb25faGVhZGVyc19jb21wbGV0ZQAEA2VudhV3YXNtX29uX21lc3NhZ2VfYmVnaW4AAANlbnYLd2FzbV9vbl91cmwAAQNlbnYOd2FzbV9vbl9zdGF0dXMAAQNlbnYUd2FzbV9vbl9oZWFkZXJfZmllbGQAAQNlbnYUd2FzbV9vbl9oZWFkZXJfdmFsdWUAAQNlbnYMd2FzbV9vbl9ib2R5AAEDZW52GHdhc21fb25fbWVzc2FnZV9jb21wbGV0ZQAAAy0sBQYAAAIAAAAAAAACAQIAAgICAAADAAAAAAMDAwMBAQEBAQEBAQEAAAIAAAAEBQFwARISBQMBAAIGCAF/AUGA1AQLB9EFIgZtZW1vcnkCAAtfaW5pdGlhbGl6ZQAIGV9faW5kaXJlY3RfZnVuY3Rpb25fdGFibGUBAAtsbGh0dHBfaW5pdAAJGGxsaHR0cF9zaG91bGRfa2VlcF9hbGl2ZQAvDGxsaHR0cF9hbGxvYwALBm1hbGxvYwAxC2xsaHR0cF9mcmVlAAwEZnJlZQAMD2xsaHR0cF9nZXRfdHlwZQANFWxsaHR0cF9nZXRfaHR0cF9tYWpvcgAOFWxsaHR0cF9nZXRfaHR0cF9taW5vcgAPEWxsaHR0cF9nZXRfbWV0aG9kABAWbGxodHRwX2dldF9zdGF0dXNfY29kZQAREmxsaHR0cF9nZXRfdXBncmFkZQASDGxsaHR0cF9yZXNldAATDmxsaHR0cF9leGVjdXRlABQUbGxodHRwX3NldHRpbmdzX2luaXQAFQ1sbGh0dHBfZmluaXNoABYMbGxodHRwX3BhdXNlABcNbGxodHRwX3Jlc3VtZQAYG2xsaHR0cF9yZXN1bWVfYWZ0ZXJfdXBncmFkZQAZEGxsaHR0cF9nZXRfZXJybm8AGhdsbGh0dHBfZ2V0X2Vycm9yX3JlYXNvbgAbF2xsaHR0cF9zZXRfZXJyb3JfcmVhc29uABwUbGxodHRwX2dldF9lcnJvcl9wb3MAHRFsbGh0dHBfZXJybm9fbmFtZQAeEmxsaHR0cF9tZXRob2RfbmFtZQAfEmxsaHR0cF9zdGF0dXNfbmFtZQAgGmxsaHR0cF9zZXRfbGVu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PgtBhwEMPQtBiAEMPAtBiQEMOwtBigEMOgtBiwEMOQtBjAEMOAtBjQEMNwtBjgEMNgtBjwEMNQtBkAEMNAtBkQEMMwtBkgEMMgtBkwEMMQtBlAEMMAtBlQEMLwtBlgEMLgtBlwEMLQtBmAEMLAtBmQEMKwtBmgEMKgtBmwEMKQtBnAEMKAtBnQEMJwtBngEMJgtBnwEMJQtBoAEMJAtBoQEMIwtBogEMIgtBowEMIQtBpAEMIAtBpQEMHwtBpgEMHgtBpwEMHQtBqAEMHAtBqQEMGwtBqgEMGgtBqwEMGQtBrAEMGAtBrQEMFwtBrgEMFgtBAQwVC0GvAQwUC0GwAQwTC0GxAQwSC0GzAQwRC0GyAQwQC0G0AQwPC0G1AQwOC0G2AQwNC0G3AQwMC0G4AQwLC0G5AQwKC0G6AQwJC0G7AQwIC0HGAQwHC0G8AQwGC0G9AQwFC0G+AQwEC0G/AQwDC0HAAQwCC0HCAQwBC0HBAQshAwNAAkACQAJAAkACQAJAAkACQAJAIAICfwJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJ/AkACQAJAAkACQAJAAkACQAJAAkACQAJAAkAgAgJ/AkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACfwJAAkACfwJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkAC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iQEhAwykAgtBASABLQAAQd8ARw1nGiABQQFqIQEMXgtBigEhAyABIARGDaICIAIoAgAiACAEIAFraiEFIAEgAGtBAWohBgNAIAEtAAAgAEGMzwBqLQAARw1kIABBAUYN+gEgAEEBaiEAIAQgAUEBaiIBRw0ACyACIAU2AgAMogILQYsBIQMgASAERg2hAiACKAIAIgAgBCABa2ohBSABIABrQQJqIQYCQANAIAEtAAAgAEGOzwBqLQAARw1kIABBAkYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAyiAgsgAkEANgIAIAZBAWohAUECDGULQYwBIQMgASAERg2gAiACKAIAIgAgBCABa2ohBSABIABrQQFqIQYCQANAIAEtAAAgAEHwzwBqLQAARw1jIABBAUYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAyhAgsgAkEANgIAIAZBAWohAUEfDGQLQY0BIQMgASAERg2fAiACKAIAIgAgBCABa2ohBSABIABrQQFqIQYCQANAIAEtAAAgAEHyzwBqLQAARw1iIABBAUYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAygAgsgAkEANgIAIAZBAWohAUEJDGMLIAEgBEYEQEGOASEDDJ8CCwJAAkAgAS0AAEHJAGsOBwBiYmJiYgFiCyABQQFqIQFB+AAhAwyGAgsgAUEBaiEBQfkAIQMMhQILQY8BIQMgASAERg2dAiACKAIAIgAgBCABa2ohBSABIABrQQVqIQYCQANAIAEtAAAgAEGRzwBqLQAARw1gIABBBUYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAyeAgsgAkEANgIAIAZBAWohAUEYDGELQZABIQMgASAERg2cAiACKAIAIgAgBCABa2ohBSABIABrQQJqIQYCQANAIAEtAAAgAEGXzwBqLQAARw1fIABBAkYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAydAgsgAkEANgIAIAZBAWohAUEXDGALQZEBIQMgASAERg2bAiACKAIAIgAgBCABa2ohBSABIABrQQZqIQYCQANAIAEtAAAgAEGazwBqLQAARw1eIABBBkYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAycAgsgAkEANgIAIAZBAWohAUEV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JSIARQ0RIAJB5QA2AhwgAiABNgIUIAIgADYCDEEAIQMMtAELIAIoAgQhACACQQA2AgQgAiAAIAEQJSIARQ0gIAJB0wA2AhwgAiABNgIUIAIgADYCDEEAIQMMswELIAIoAgQhACACQQA2AgQgAiAAIAEQJSIARQ0iIAJB0gA2AhwgAiABNgIUIAIgADYCDEEAIQMMsgELIAIoAgQhACACQQA2AgQgAiAAIAEQJSIARQ0OIAJB5QA2AhwgAiABNgIUIAIgADYCDEEAIQMMsQELIAIoAgQhACACQQA2AgQgAiAAIAEQJSIARQ0dIAJB0wA2AhwgAiABNgIUIAIgADYCDEEAIQMMsAELIAIoAgQhACACQQA2AgQgAiAAIAEQJSIARQ0fIAJB0gA2AhwgAiABNgIUIAIgADYCDEEAIQMMrwELIABBP0cNASABQQFqCyEBQQUhAwyUAQtBACEDIAJBADYCHCACIAE2AhQgAkH9EjYCECACQQc2AgwMrAELIAJBADYCHCACIAE2AhQgAkHcCDYCECACQQc2AgxBACEDDKsBCyACKAIEIQAgAkEANgIEIAIgACABECUiAEUNByACQeUANgIcIAIgATYCFCACIAA2AgxBACEDDKoBCyACKAIEIQAgAkEANgIEIAIgACABECUiAEUNFiACQdMANgIcIAIgATYCFCACIAA2AgxBACEDDKkBCyACKAIEIQAgAkEANgIEIAIgACABECUiAEUNGCACQdIANgIcIAIgATYCFCACIAA2AgxBACEDDKgBCyACQQA2AhwgAiABNgIUIAJBxgo2AhAgAkEHNgIMQQAhAwynAQsgAigCBCEAIAJBADYCBCACIAAgARAlIgBFDQMgAkHlADYCHCACIAE2AhQgAiAANgIMQQAhAwymAQsgAigCBCEAIAJBADYCBCACIAAgARAlIgBFDRIgAkHTADYCHCACIAE2AhQgAiAANgIMQQAhAwylAQsgAigCBCEAIAJBADYCBCACIAAgARAlIgBFDRQgAkHSADYCHCACIAE2AhQgAiAANgIMQQAhAwykAQsgAigCBCEAIAJBADYCBCACIAAgARAlIgBFDQAgAkHlADYCHCACIAE2AhQgAiAANgIMQQAhAwyjAQtB1QAhAwyJAQsgAEEVRwRAIAJBADYCHCACIAE2AhQgAkG5DTYCECACQRo2AgxB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AkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAIABB5ABrDvQDY2IAAWFhYWFhYQIDBAVhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhBgcICQoLDA0OD2FhYWFhEGFhYWFhYWFhYWFhEWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYRITFBUWFxgZGhthYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhHB0eHyAhIiMkJSYnKCkqKywtLi8wMTIzNDU2YTc4OTphYWFhYWFhYTthYWE8YWFhYT0+P2FhYWFhYWFhQGFhQWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYUJDREVGR0hJSktMTU5PUFFSU2FhYWFhYWFhVFVWV1hZWlthXF1hYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFeYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhX2BhC0HhJw8LQaQhDwtByywPC0H+MQ8LQcAkDwtBqyQPC0GNKA8LQeImDwtBgDAPC0G5Lw8LQdckDwtB7x8PC0HhHw8LQfofDwtB8iAPC0GoLw8LQa4yDwtBiDAPC0HsJw8LQYIiDwtBjh0PC0HQLg8LQcojDwtBxTIPC0HfHA8LQdIcDwtBxCAPC0HXIA8LQaIfDwtB7S4PC0GrMA8LQdQlDwtBzC4PC0H6Lg8LQfwrDwtB0jAPC0HxHQ8LQbsgDwtB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CABB3AgLii1JbnZhbGlkIGNoYXIgaW4gdXJsIHF1ZXJ5AFNwYW4gY2FsbGJhY2sgZXJyb3IgaW4gb25fYm9keQBDb250ZW50LUxlbmd0aCBvdmVyZmxvdwBDaHVuayBzaXplIG92ZXJmbG93AFJlc3BvbnNlIG92ZXJmbG93AEludmFsaWQgbWV0aG9kIGZvciBIVFRQL3gueCByZXF1ZXN0AEludmFsaWQgbWV0aG9kIGZvciBSVFNQL3gueCByZXF1ZXN0AEV4cGVjdGVkIFNPVVJDRSBtZXRob2QgZm9yIElDRS94LnggcmVxdWVzdABJbnZhbGlkIGNoYXIgaW4gdXJsIGZyYWdtZW50IHN0YXJ0AEV4cGVjdGVkIGRvdABTcGFuIGNhbGxiYWNrIGVycm9yIGluIG9uX3N0YXR1cwBJbnZhbGlkIHJlc3BvbnNlIHN0YXR1cwBJbnZhbGlkIGNoYXJhY3RlciBpbiBjaHVuayBleHRlbnNpb25zAFVzZXIgY2FsbGJhY2sgZXJyb3IAYG9uX3Jlc2V0YCBjYWxsYmFjayBlcnJvcgBgb25fY2h1bmtfaGVhZGVyYCBjYWxsYmFjayBlcnJvcgBgb25fbWVzc2FnZV9iZWdpbmAgY2FsbGJhY2sgZXJyb3IAYG9uX2NodW5rX2V4dGVuc2lvbl92YWx1ZWAgY2FsbGJhY2sgZXJyb3IAYG9uX3N0YXR1c19jb21wbGV0ZWAgY2FsbGJhY2sgZXJyb3IAYG9uX3ZlcnNpb25fY29tcGxldGVgIGNhbGxiYWNrIGVycm9yAGBvbl91cmxfY29tcGxldGVgIGNhbGxiYWNrIGVycm9yAGBvbl9jaHVua19jb21wbGV0ZWAgY2FsbGJhY2sgZXJyb3IAYG9uX2hlYWRlcl92YWx1ZV9jb21wbGV0ZWAgY2FsbGJhY2sgZXJyb3IAYG9uX21lc3NhZ2VfY29tcGxldGVgIGNhbGxiYWNrIGVycm9yAGBvbl9tZXRob2RfY29tcGxldGVgIGNhbGxiYWNrIGVycm9yAGBvbl9oZWFkZXJfZmllbGRfY29tcGxldGVgIGNhbGxiYWNrIGVycm9yAGBvbl9jaHVua19leHRlbnNpb25fbmFtZWAgY2FsbGJhY2sgZXJyb3IAVW5leHBlY3RlZCBjaGFyIGluIHVybCBzZXJ2ZXIASW52YWxpZCBoZWFkZXIgdmFsdWUgY2hhcgBJbnZhbGlkIGhlYWRlciBmaWVsZCBjaGFyAFNwYW4gY2FsbGJhY2sgZXJyb3IgaW4gb25fdmVyc2lvbgBJbnZhbGlkIG1pbm9yIHZlcnNpb24ASW52YWxpZCBtYWpvciB2ZXJzaW9uAEV4cGVjdGVkIHNwYWNlIGFmdGVyIHZlcnNpb24ARXhwZWN0ZWQgQ1JMRiBhZnRlciB2ZXJzaW9uAEludmFsaWQgSFRUUCB2ZXJzaW9uAEludmFsaWQgaGVhZGVyIHRva2VuAFNwYW4gY2FsbGJhY2sgZXJyb3IgaW4gb25fdXJsAEludmFsaWQgY2hhcmFjdGVycyBpbiB1cmwAVW5leHBlY3RlZCBzdGFydCBjaGFyIGluIHVybABEb3VibGUgQCBpbiB1cmwARW1wdHkgQ29udGVudC1MZW5ndGgASW52YWxpZCBjaGFyYWN0ZXIgaW4gQ29udGVudC1MZW5ndGgARHVwbGljYXRlIENvbnRlbnQtTGVuZ3RoAEludmFsaWQgY2hhciBpbiB1cmwgcGF0aABDb250ZW50LUxlbmd0aCBjYW4ndCBiZSBwcmVzZW50IHdpdGggVHJhbnNmZXItRW5jb2RpbmcASW52YWxpZCBjaGFyYWN0ZXIgaW4gY2h1bmsgc2l6ZQBTcGFuIGNhbGxiYWNrIGVycm9yIGluIG9uX2hlYWRlcl92YWx1ZQBTcGFuIGNhbGxiYWNrIGVycm9yIGluIG9uX2NodW5rX2V4dGVuc2lvbl92YWx1ZQBJbnZhbGlkIGNoYXJhY3RlciBpbiBjaHVuayBleHRlbnNpb25zIHZhbHVlAE1pc3NpbmcgZXhwZWN0ZWQgTEYgYWZ0ZXIgaGVhZGVyIHZhbHVlAEludmFsaWQgYFRyYW5zZmVyLUVuY29kaW5nYCBoZWFkZXIgdmFsdWUASW52YWxpZCBjaGFyYWN0ZXIgaW4gY2h1bmsgZXh0ZW5zaW9ucyBxdW90ZSB2YWx1ZQBJbnZhbGlkIGNoYXJhY3RlciBpbiBjaHVuayBleHRlbnNpb25zIHF1b3RlZCB2YWx1ZQBQYXVzZWQgYnkgb25faGVhZGVyc19jb21wbGV0ZQBJbnZhbGlkIEVPRiBzdGF0ZQBvbl9yZXNldCBwYXVzZQBvbl9jaHVua19oZWFkZXIgcGF1c2UAb25fbWVzc2FnZV9iZWdpbiBwYXVzZQBvbl9jaHVua19leHRlbnNpb25fdmFsdWUgcGF1c2UAb25fc3RhdHVzX2NvbXBsZXRlIHBhdXNlAG9uX3ZlcnNpb25fY29tcGxldGUgcGF1c2UAb25fdXJsX2NvbXBsZXRlIHBhdXNlAG9uX2NodW5rX2NvbXBsZXRlIHBhdXNlAG9uX2hlYWRlcl92YWx1ZV9jb21wbGV0ZSBwYXVzZQBvbl9tZXNzYWdlX2NvbXBsZXRlIHBhdXNlAG9uX21ldGhvZF9jb21wbGV0ZSBwYXVzZQBvbl9oZWFkZXJfZmllbGRfY29tcGxldGUgcGF1c2UAb25fY2h1bmtfZXh0ZW5zaW9uX25hbWUgcGF1c2UAVW5leHBlY3RlZCBzcGFjZSBhZnRlciBzdGFydCBsaW5lAFNwYW4gY2FsbGJhY2sgZXJyb3IgaW4gb25fY2h1bmtfZXh0ZW5zaW9uX25hbWUASW52YWxpZCBjaGFyYWN0ZXIgaW4gY2h1bmsgZXh0ZW5zaW9ucyBuYW1lAFBhdXNlIG9uIENPTk5FQ1QvVXBncmFkZQBQYXVzZSBvbiBQUkkvVXBncmFkZQBFeHBlY3RlZCBIVFRQLzIgQ29ubmVjdGlvbiBQcmVmYWNlAFNwYW4gY2FsbGJhY2sgZXJyb3IgaW4gb25fbWV0aG9kAEV4cGVjdGVkIHNwYWNlIGFmdGVyIG1ldGhvZABTcGFuIGNhbGxiYWNrIGVycm9yIGluIG9uX2hlYWRlcl9maWVsZABQYXVzZWQASW52YWxpZCB3b3JkIGVuY291bnRlcmVkAEludmFsaWQgbWV0aG9kIGVuY291bnRlcmVkAFVuZXhwZWN0ZWQgY2hhciBpbiB1cmwgc2NoZW1hAFJlcXVlc3QgaGFzIGludmFsaWQgYFRyYW5zZmVyLUVuY29kaW5nYABTV0lUQ0hfUFJPWFkAVVNFX1BST1hZAE1LQUNUSVZJVFkAVU5QUk9DRVNTQUJMRV9FTlRJVFkAQ09QWQBNT1ZFRF9QRVJNQU5FTlRMWQBUT09fRUFSTFkATk9USUZZAEZBSUxFRF9ERVBFTkRFTkNZAEJBRF9HQVRFV0FZAFBMQVkAUFVUAENIRUNLT1VUAEdBVEVXQVlfVElNRU9VVABSRVFVRVNUX1RJTUVPVVQATkVUV09SS19DT05ORUNUX1RJTUVPVVQAQ09OTkVDVElPTl9USU1FT1VUAExPR0lOX1RJTUVPVVQATkVUV09SS19SRUFEX1RJTUVPVVQAUE9TVABNSVNESVJFQ1RFRF9SRVFVRVNUAENMSUVOVF9DTE9TRURfUkVRVUVTVABDTElFTlRfQ0xPU0VEX0xPQURfQkFMQU5DRURfUkVRVUVTVABCQURfUkVRVUVTVABIVFRQX1JFUVVFU1RfU0VOVF9UT19IVFRQU19QT1JUAFJFUE9SVABJTV9BX1RFQVBPVABSRVNFVF9DT05URU5UAE5PX0NPTlRFTlQAUEFSVElBTF9DT05URU5UAEhQRV9JTlZBTElEX0NPTlNUQU5UAEhQRV9DQl9SRVNFVABHRVQASFBFX1NUUklDVABDT05GTElDVABURU1QT1JBUllfUkVESVJFQ1QAUEVSTUFORU5UX1JFRElSRUNUAENPTk5FQ1QATVVMVElfU1RBVFVTAEhQRV9JTlZBTElEX1NUQVRVUwBUT09fTUFOWV9SRVFVRVNUUwBFQVJMWV9ISU5UUwBVTkFWQUlMQUJMRV9GT1JfTEVHQUxfUkVBU09OUwBPUFRJT05TAFNXSVRDSElOR19QUk9UT0NPTFMAVkFSSUFOVF9BTFNPX05FR09USUFURVMATVVMVElQTEVfQ0hPSUNFUwBJTlRFUk5BTF9TRVJWRVJfRVJST1IAV0VCX1NFUlZFUl9VTktOT1dOX0VSUk9SAFJBSUxHVU5fRVJST1IASURFTlRJVFlfUFJPVklERVJfQVVUSEVOVElDQVRJT05fRVJST1IAU1NMX0NFUlRJRklDQVRFX0VSUk9SAElOVkFMSURfWF9GT1JXQVJERURfRk9SAFNFVF9QQVJBTUVURVIAR0VUX1BBUkFNRVRFUgBIUEVfVVNFUgBTRUVfT1RIRVIASFBFX0NCX0NIVU5LX0hFQURFUgBNS0NBTEVOREFSAFNFVFVQAFdFQl9TRVJWRVJfSVNfRE9XTgBURUFSRE9XTgBIUEVfQ0xPU0VEX0NPTk5FQ1RJT04ASEVVUklTVElDX0VYUElSQVRJT04ARElTQ09OTkVDVEVEX09QRVJBVElPTgBOT05fQVVUSE9SSVRBVElWRV9JTkZPUk1BVElPTgBIUEVfSU5WQUxJRF9WRVJTSU9OAEhQRV9DQl9NRVNTQUdFX0JFR0lOAFNJVEVfSVNfRlJPWkVOAEhQRV9JTlZBTElEX0hFQURFUl9UT0tFTgBJTlZBTElEX1RPS0VOAEZPUkJJRERFTgBFTkhBTkNFX1lPVVJfQ0FMTQBIUEVfSU5WQUxJRF9VUkwAQkxPQ0tFRF9CWV9QQVJFTlRBTF9DT05UUk9MAE1LQ09MAEFDTABIUEVfSU5URVJOQUwAUkVRVUVTVF9IRUFERVJfRklFTERTX1RPT19MQVJHRV9VTk9GRklDSUFMAEhQRV9PSwBVTkxJTksAVU5MT0NLAFBSSQBSRVRSWV9XSVRIAEhQRV9JTlZBTElEX0NPTlRFTlRfTEVOR1RIAEhQRV9VTkVYUEVDVEVEX0NPTlRFTlRfTEVOR1RIAEZMVVNIAFBST1BQQVRDSABNLVNFQVJDSABVUklfVE9PX0xPTkcAUFJPQ0VTU0lORwBNSVNDRUxMQU5FT1VTX1BFUlNJU1RFTlRfV0FSTklORwBNSVNDRUxMQU5FT1VTX1dBUk5JTkcASFBFX0lOVkFMSURfVFJBTlNGRVJfRU5DT0RJTkcARXhwZWN0ZWQgQ1JMRgBIUEVfSU5WQUxJRF9DSFVOS19TSVpFAE1PVkUAQ09OVElOVUUASFBFX0NCX1NUQVRVU19DT01QTEVURQBIUEVfQ0JfSEVBREVSU19DT01QTEVURQBIUEVfQ0JfVkVSU0lPTl9DT01QTEVURQBIUEVfQ0JfVVJMX0NPTVBMRVRFAEhQRV9DQl9DSFVOS19DT01QTEVURQBIUEVfQ0JfSEVBREVSX1ZBTFVFX0NPTVBMRVRFAEhQRV9DQl9DSFVOS19FWFRFTlNJT05fVkFMVUVfQ09NUExFVEUASFBFX0NCX0NIVU5LX0VYVEVOU0lPTl9OQU1FX0NPTVBMRVRFAEhQRV9DQl9NRVNTQUdFX0NPTVBMRVRFAEhQRV9DQl9NRVRIT0RfQ09NUExFVEUASFBFX0NCX0hFQURFUl9GSUVMRF9DT01QTEVURQBERUxFVEUASFBFX0lOVkFMSURfRU9GX1NUQVRFAElOVkFMSURfU1NMX0NFUlRJRklDQVRFAFBBVVNFAE5PX1JFU1BPTlNFAFVOU1VQUE9SVEVEX01FRElBX1RZUEUAR09ORQBOT1RfQUNDRVBUQUJMRQBTRVJWSUNFX1VOQVZBSUxBQkxFAFJBTkdFX05PVF9TQVRJU0ZJQUJMRQBPUklHSU5fSVNfVU5SRUFDSEFCTEUAUkVTUE9OU0VfSVNfU1RBTEUAUFVSR0UATUVSR0UAUkVRVUVTVF9IRUFERVJfRklFTERTX1RPT19MQVJHRQBSRVFVRVNUX0hFQURFUl9UT09fTEFSR0UAUEFZTE9BRF9UT09fTEFSR0UASU5TVUZGSUNJRU5UX1NUT1JBR0UASFBFX1BBVVNFRF9VUEdSQURFAEhQRV9QQVVTRURfSDJfVVBHUkFERQBTT1VSQ0UAQU5OT1VOQ0UAVFJBQ0UASFBFX1VORVhQRUNURURfU1BBQ0UAREVTQ1JJQkUAVU5TVUJTQ1JJQkUAUkVDT1JEAEhQRV9JTlZBTElEX01FVEhPRABOT1RfRk9VTkQAUFJPUEZJTkQAVU5CSU5EAFJFQklORABVTkFVVEhPUklaRUQATUVUSE9EX05PVF9BTExPV0VEAEhUVFBfVkVSU0lPTl9OT1RfU1VQUE9SVEVEAEFMUkVBRFlfUkVQT1JURUQAQUNDRVBURUQATk9UX0lNUExFTUVOVEVEAExPT1BfREVURUNURUQASFBFX0NSX0VYUEVDVEVEAEhQRV9MRl9FWFBFQ1RFRABDUkVBVEVEAElNX1VTRUQASFBFX1BBVVNFRABUSU1FT1VUX09DQ1VSRUQAUEFZTUVOVF9SRVFVSVJFRABQUkVDT05ESVRJT05fUkVRVUlSRUQAUFJPWFlfQVVUSEVOVElDQVRJT05fUkVRVUlSRUQATkVUV09SS19BVVRIRU5USUNBVElPTl9SRVFVSVJFRABMRU5HVEhfUkVRVUlSRUQAU1NMX0NFUlRJRklDQVRFX1JFUVVJUkVEAFVQR1JBREVfUkVRVUlSRUQAUEFHRV9FWFBJUkVEAFBSRUNPTkRJVElPTl9GQUlMRUQARVhQRUNUQVRJT05fRkFJTEVEAFJFVkFMSURBVElPTl9GQUlMRUQAU1NMX0hBTkRTSEFLRV9GQUlMRUQATE9DS0VEAFRSQU5TRk9STUFUSU9OX0FQUExJRUQATk9UX01PRElGSUVEAE5PVF9FWFRFTkRFRABCQU5EV0lEVEhfTElNSVRfRVhDRUVERUQAU0lURV9JU19PVkVSTE9BREVEAEhFQUQARXhwZWN0ZWQgSFRUUC8AAF4TAAAmEwAAMBAAAPAXAACdEwAAFRIAADkXAADwEgAAChAAAHUSAACtEgAAghMAAE8UAAB/EAAAoBUAACMUAACJEgAAixQAAE0VAADUEQAAzxQAABAYAADJFgAA3BYAAMERAADgFwAAuxQAAHQUAAB8FQAA5RQAAAgXAAAfEAAAZRUAAKMUAAAoFQAAAhUAAJkVAAAsEAAAixkAAE8PAADUDgAAahAAAM4QAAACFwAAiQ4AAG4TAAAcEwAAZhQAAFYXAADBEwAAzRMAAGwTAABoFwAAZhcAAF8XAAAiEwAAzg8AAGkOAADYDgAAYxYAAMsTAACqDgAAKBcAACYXAADFEwAAXRYAAOgRAABnEwAAZRMAAPIWAABzEwAAHRcAAPkWAADzEQAAzw4AAM4VAAAMEgAAsxEAAKURAABhEAAAMhcAALsTAEH5NQsBAQBBkDYL4AEBAQIBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEAAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQBB/TcLAQEAQZE4C14CAwICAgICAAACAgACAgACAgICAgICAgICAAQAAAAAAAICAgICAgICAgICAgICAgICAgICAgICAgICAAAAAgICAgICAgICAgICAgICAgICAgICAgICAgICAgIAAgACAEH9OQsBAQBBkToLXgIAAgICAgIAAAICAAICAAICAgICAgICAgIAAwAEAAAAAgICAgICAgICAgICAgICAgICAgICAgICAgIAAAACAgICAgICAgICAgICAgICAgICAgICAgICAgICAgACAAIAQfA7Cw1sb3NlZWVwLWFsaXZlAEGJPAsBAQBBoDwL4AEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQABAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQBBiT4LAQEAQaA+C+cBAQEBAQEBAQEBAQEBAgEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEAAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQFjaHVua2VkAEGwwAALXwEBAAEBAQEBAAABAQABAQABAQEBAQEBAQEBAAAAAAAAAAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAAAAAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEAAQABAEGQwgALIWVjdGlvbmVudC1sZW5ndGhvbnJveHktY29ubmVjdGlvbgBBwMIACy1yYW5zZmVyLWVuY29kaW5ncGdyYWRlDQoNCg0KU00NCg0KVFRQL0NFL1RTUC8AQfnCAAsFAQIAAQMAQZDDAAvgAQQBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAEH5xAALBQECAAEDAEGQxQAL4AEEAQEFAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQABAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQBB+cYACwQBAAABAEGRxwAL3wEBAQABAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEAAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAEH6yAALBAEAAAIAQZDJAAtfAwQAAAQEBAQEBAQEBAQEBQQEBAQEBAQEBAQEBAAEAAYHBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEAAQABAAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAAAAAQAQfrKAAsEAQAAAQBBkMsACwEBAEGqywALQQIAAAAAAAADAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwAAAAAAAAMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAEH6zAALBAEAAAEAQZDNAAsBAQBBms0ACwYCAAAAAAIAQbHNAAs6AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMAAAAAAAADAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwBB8M4AC5YBTk9VTkNFRUNLT1VUTkVDVEVURUNSSUJFTFVTSEVURUFEU0VBUkNIUkdFQ1RJVklUWUxFTkRBUlZFT1RJRllQVElPTlNDSFNFQVlTVEFUQ0hHRU9SRElSRUNUT1JUUkNIUEFSQU1FVEVSVVJDRUJTQ1JJQkVBUkRPV05BQ0VJTkROS0NLVUJTQ1JJQkVIVFRQL0FEVFAv", "base64");
});

// node_modules/undici/lib/llhttp/llhttp_simd-wasm.js
var require_llhttp_simd_wasm = __commonJS((exports, module) => {
  var { Buffer: Buffer2 } = __require("buffer");
  module.exports = Buffer2.from("AGFzbQEAAAABJwdgAX8Bf2ADf39/AX9gAX8AYAJ/fwBgBH9/f38Bf2AAAGADf39/AALLAQgDZW52GHdhc21fb25faGVhZGVyc19jb21wbGV0ZQAEA2VudhV3YXNtX29uX21lc3NhZ2VfYmVnaW4AAANlbnYLd2FzbV9vbl91cmwAAQNlbnYOd2FzbV9vbl9zdGF0dXMAAQNlbnYUd2FzbV9vbl9oZWFkZXJfZmllbGQAAQNlbnYUd2FzbV9vbl9oZWFkZXJfdmFsdWUAAQNlbnYMd2FzbV9vbl9ib2R5AAEDZW52GHdhc21fb25fbWVzc2FnZV9jb21wbGV0ZQAAAy0sBQYAAAIAAAAAAAACAQIAAgICAAADAAAAAAMDAwMBAQEBAQEBAQEAAAIAAAAEBQFwARISBQMBAAIGCAF/AUGA1AQLB9EFIgZtZW1vcnkCAAtfaW5pdGlhbGl6ZQAIGV9faW5kaXJlY3RfZnVuY3Rpb25fdGFibGUBAAtsbGh0dHBfaW5pdAAJGGxsaHR0cF9zaG91bGRfa2VlcF9hbGl2ZQAvDGxsaHR0cF9hbGxvYwALBm1hbGxvYwAxC2xsaHR0cF9mcmVlAAwEZnJlZQAMD2xsaHR0cF9nZXRfdHlwZQANFWxsaHR0cF9nZXRfaHR0cF9tYWpvcgAOFWxsaHR0cF9nZXRfaHR0cF9taW5vcgAPEWxsaHR0cF9nZXRfbWV0aG9kABAWbGxodHRwX2dldF9zdGF0dXNfY29kZQAREmxsaHR0cF9nZXRfdXBncmFkZQASDGxsaHR0cF9yZXNldAATDmxsaHR0cF9leGVjdXRlABQUbGxodHRwX3NldHRpbmdzX2luaXQAFQ1sbGh0dHBfZmluaXNoABYMbGxodHRwX3BhdXNlABcNbGxodHRwX3Jlc3VtZQAYG2xsaHR0cF9yZXN1bWVfYWZ0ZXJfdXBncmFkZQAZEGxsaHR0cF9nZXRfZXJybm8AGhdsbGh0dHBfZ2V0X2Vycm9yX3JlYXNvbgAbF2xsaHR0cF9zZXRfZXJyb3JfcmVhc29uABwUbGxodHRwX2dldF9lcnJvcl9wb3MAHRFsbGh0dHBfZXJybm9fbmFtZQAeEmxsaHR0cF9tZXRob2RfbmFtZQAfEmxsaHR0cF9zdGF0dXNfbmFtZQAgGmxsaHR0cF9zZXRfbGVu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PgtBhwEMPQtBiAEMPAtBiQEMOwtBigEMOgtBiwEMOQtBjAEMOAtBjQEMNwtBjgEMNgtBjwEMNQtBkAEMNAtBkQEMMwtBkgEMMgtBkwEMMQtBlAEMMAtBlQEMLwtBlgEMLgtBlwEMLQtBmAEMLAtBmQEMKwtBmgEMKgtBmwEMKQtBnAEMKAtBnQEMJwtBngEMJgtBnwEMJQtBoAEMJAtBoQEMIwtBogEMIgtBowEMIQtBpAEMIAtBpQEMHwtBpgEMHgtBpwEMHQtBqAEMHAtBqQEMGwtBqgEMGgtBqwEMGQtBrAEMGAtBrQEMFwtBrgEMFgtBAQwVC0GvAQwUC0GwAQwTC0GxAQwSC0GzAQwRC0GyAQwQC0G0AQwPC0G1AQwOC0G2AQwNC0G3AQwMC0G4AQwLC0G5AQwKC0G6AQwJC0G7AQwIC0HGAQwHC0G8AQwGC0G9AQwFC0G+AQwEC0G/AQwDC0HAAQwCC0HCAQwBC0HBAQshAwNAAkACQAJAAkACQAJAAkACQAJAIAICfwJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJ/AkACQAJAAkACQAJAAkACQAJAAkACQAJAAkAgAgJ/AkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACfwJAAkACfwJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkAC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iQEhAwykAgtBASABLQAAQd8ARw1nGiABQQFqIQEMXgtBigEhAyABIARGDaICIAIoAgAiACAEIAFraiEFIAEgAGtBAWohBgNAIAEtAAAgAEGMzwBqLQAARw1kIABBAUYN+gEgAEEBaiEAIAQgAUEBaiIBRw0ACyACIAU2AgAMogILQYsBIQMgASAERg2hAiACKAIAIgAgBCABa2ohBSABIABrQQJqIQYCQANAIAEtAAAgAEGOzwBqLQAARw1kIABBAkYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAyiAgsgAkEANgIAIAZBAWohAUECDGULQYwBIQMgASAERg2gAiACKAIAIgAgBCABa2ohBSABIABrQQFqIQYCQANAIAEtAAAgAEHwzwBqLQAARw1jIABBAUYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAyhAgsgAkEANgIAIAZBAWohAUEfDGQLQY0BIQMgASAERg2fAiACKAIAIgAgBCABa2ohBSABIABrQQFqIQYCQANAIAEtAAAgAEHyzwBqLQAARw1iIABBAUYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAygAgsgAkEANgIAIAZBAWohAUEJDGMLIAEgBEYEQEGOASEDDJ8CCwJAAkAgAS0AAEHJAGsOBwBiYmJiYgFiCyABQQFqIQFB+AAhAwyGAgsgAUEBaiEBQfkAIQMMhQILQY8BIQMgASAERg2dAiACKAIAIgAgBCABa2ohBSABIABrQQVqIQYCQANAIAEtAAAgAEGRzwBqLQAARw1gIABBBUYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAyeAgsgAkEANgIAIAZBAWohAUEYDGELQZABIQMgASAERg2cAiACKAIAIgAgBCABa2ohBSABIABrQQJqIQYCQANAIAEtAAAgAEGXzwBqLQAARw1fIABBAkYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAydAgsgAkEANgIAIAZBAWohAUEXDGALQZEBIQMgASAERg2bAiACKAIAIgAgBCABa2ohBSABIABrQQZqIQYCQANAIAEtAAAgAEGazwBqLQAARw1eIABBBkYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAycAgsgAkEANgIAIAZBAWohAUEVDF8LQZIBIQMgASAERg2aAiACKAIAIgAgBCABa2ohBSABIABrQQVqIQYCQANAIAEtAAAgAEGhzwBqLQAARw1dIABBBUYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAybAgsgAkEANgIAIAZBAWohAUEeDF4LIAEgBEYEQEGTASEDDJoCCyABLQAAQcwARw1bIAFBAWohAUEKDF0LIAEgBEYEQEGUASEDDJkCCwJAAkAgAS0AAEHBAGsODwBcXFxcXFxcXFxcXFxcAVwLIAFBAWohAUH+ACEDDIACCyABQQFqIQFB/wAhAwz/AQsgASAERgRAQZUBIQMMmAILAkACQCABLQAAQcEAaw4DAFsBWwsgAUEBaiEBQf0AIQMM/wELIAFBAWohAUGAASEDDP4BC0GWASEDIAEgBEYNlgIgAigCACIAIAQgAWtqIQUgASAAa0EBaiEGAkADQCABLQAAIABBp88Aai0AAEcNWSAAQQFGDQEgAEEBaiEAIAQgAUEBaiIBRw0ACyACIAU2AgAMlwILIAJBADYCACAGQQFqIQFBCwxaCyABIARGBEBBlwEhAwyWAgsCQAJAAkACQCABLQAAQS1rDiMAW1tbW1tbW1tbW1tbW1tbW1tbW1tbW1sBW1tbW1sCW1tbA1sLIAFBAWohAUH7ACEDDP8BCyABQQFqIQFB/AAhAwz+AQsgAUEBaiEBQYEBIQMM/QELIAFBAWohAUGCASEDDPwBC0GYASEDIAEgBEYNlAIgAigCACIAIAQgAWtqIQUgASAAa0EEaiEGAkADQCABLQAAIABBqc8Aai0AAEcNVyAAQQRGDQEgAEEBaiEAIAQgAUEBaiIBRw0ACyACIAU2AgAMlQILIAJBADYCACAGQQFqIQFBGQxYC0GZASEDIAEgBEYNkwIgAigCACIAIAQgAWtqIQUgASAAa0EFaiEGAkADQCABLQAAIABBrs8Aai0AAEcNViAAQQVGDQEgAEEBaiEAIAQgAUEBaiIBRw0ACyACIAU2AgAMlAILIAJBADYCACAGQQFqIQFBBgxXC0GaASEDIAEgBEYNkgIgAigCACIAIAQgAWtqIQUgASAAa0EBaiEGAkADQCABLQAAIABBtM8Aai0AAEcNVSAAQQFGDQEgAEEBaiEAIAQgAUEBaiIBRw0ACyACIAU2AgAMkwILIAJBADYCACAGQQFqIQFBHAxWC0GbASEDIAEgBEYNkQIgAigCACIAIAQgAWtqIQUgASAAa0EBaiEGAkADQCABLQAAIABBts8Aai0AAEcNVCAAQQFGDQEgAEEBaiEAIAQgAUEBaiIBRw0ACyACIAU2AgAMkgILIAJBADYCACAGQQFqIQFBJwxVCyABIARGBEBBnAEhAwyRAgsCQAJAIAEtAABB1ABrDgIAAVQLIAFBAWohAUGGASEDDPgBCyABQQFqIQFBhwEhAwz3AQtBnQEhAyABIARGDY8CIAIoAgAiACAEIAFraiEFIAEgAGtBAWohBgJAA0AgAS0AACAAQbjPAGotAABHDVIgAEEBRg0BIABBAWohACAEIAFBAWoiAUcNAAsgAiAFNgIADJACCyACQQA2AgAgBkEBaiEBQSYMUwtBngEhAyABIARGDY4CIAIoAgAiACAEIAFraiEFIAEgAGtBAWohBgJAA0AgAS0AACAAQbrPAGotAABHDVEgAEEBRg0BIABBAWohACAEIAFBAWoiAUcNAAsgAiAFNgIADI8CCyACQQA2AgAgBkEBaiEBQQMMUgtBnwEhAyABIARGDY0CIAIoAgAiACAEIAFraiEFIAEgAGtBAmohBgJAA0AgAS0AACAAQe3PAGotAABHDVAgAEECRg0BIABBAWohACAEIAFBAWoiAUcNAAsgAiAFNgIADI4CCyACQQA2AgAgBkEBaiEBQQwMUQtBoAEhAyABIARGDYwCIAIoAgAiACAEIAFraiEFIAEgAGtBA2ohBgJAA0AgAS0AACAAQbzPAGotAABHDU8gAEEDRg0BIABBAWohACAEIAFBAWoiAUcNAAsgAiAFNgIADI0CCyACQQA2AgAgBkEBaiEBQQ0MUAsgASAERgRAQaEBIQMMjAILAkACQCABLQAAQcYAaw4LAE9PT09PT09PTwFPCyABQQFqIQFBiwEhAwzzAQsgAUEBaiEBQYwBIQMM8gELIAEgBEYEQEGiASEDDIsCCyABLQAAQdAARw1MIAFBAWohAQxGCyABIARGBEBBowEhAwyKAgsCQAJAIAEtAABByQBrDgcBTU1NTU0ATQsgAUEBaiEBQY4BIQMM8QELIAFBAWohAUEiDE0LQaQBIQMgASAERg2IAiACKAIAIgAgBCABa2ohBSABIABrQQFqIQYCQANAIAEtAAAgAEHAzwBqLQAARw1LIABBAUYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAyJAgsgAkEANgIAIAZBAWohAUEdDEwLIAEgBEYEQEGlASEDDIgCCwJAAkAgAS0AAEHSAGsOAwBLAUsLIAFBAWohAUGQASEDDO8BCyABQQFqIQFBBAxLCyABIARGBEBBpgEhAwyHAgsCQAJAAkACQAJAIAEtAABBwQBrDhUATU1NTU1NTU1NTQFNTQJNTQNNTQRNCyABQQFqIQFBiAEhAwzxAQsgAUEBaiEBQYkBIQMM8AELIAFBAWohAUGKASEDDO8BCyABQQFqIQFBjwEhAwzuAQsgAUEBaiEBQZEBIQMM7QELQacBIQMgASAERg2FAiACKAIAIgAgBCABa2ohBSABIABrQQJqIQYCQANAIAEtAAAgAEHtzwBqLQAARw1IIABBAkYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAyGAgsgAkEANgIAIAZBAWohAUERDEkLQagBIQMgASAERg2EAiACKAIAIgAgBCABa2ohBSABIABrQQJqIQYCQANAIAEtAAAgAEHCzwBqLQAARw1HIABBAkYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAyFAgsgAkEANgIAIAZBAWohAUEsDEgLQakBIQMgASAERg2DAiACKAIAIgAgBCABa2ohBSABIABrQQRqIQYCQANAIAEtAAAgAEHFzwBqLQAARw1GIABBBEYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAyEAgsgAkEANgIAIAZBAWohAUErDEcLQaoBIQMgASAERg2CAiACKAIAIgAgBCABa2ohBSABIABrQQJqIQYCQANAIAEtAAAgAEHKzwBqLQAARw1FIABBAkYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAyDAgsgAkEANgIAIAZBAWohAUEUDEYLIAEgBEYEQEGrASEDDIICCwJAAkACQAJAIAEtAABBwgBrDg8AAQJHR0dHR0dHR0dHRwNHCyABQQFqIQFBkwEhAwzrAQsgAUEBaiEBQZQBIQMM6gELIAFBAWohAUGVASEDDOkBCyABQQFqIQFBlgEhAwzoAQsgASAERgRAQawBIQMMgQILIAEtAABBxQBHDUIgAUEBaiEBDD0LQa0BIQMgASAERg3/ASACKAIAIgAgBCABa2ohBSABIABrQQJqIQYCQANAIAEtAAAgAEHNzwBqLQAARw1CIABBAkYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAyAAgsgAkEANgIAIAZBAWohAUEODEMLIAEgBEYEQEGuASEDDP8BCyABLQAAQdAARw1AIAFBAWohAUElDEILQa8BIQMgASAERg39ASACKAIAIgAgBCABa2ohBSABIABrQQhqIQYCQANAIAEtAAAgAEHQzwBqLQAARw1AIABBCEYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAz+AQsgAkEANgIAIAZBAWohAUEqDEELIAEgBEYEQEGwASEDDP0BCwJAAkAgAS0AAEHVAGsOCwBAQEBAQEBAQEABQAsgAUEBaiEBQZoBIQMM5AELIAFBAWohAUGbASEDDOMBCyABIARGBEBBsQEhAwz8AQsCQAJAIAEtAABBwQBrDhQAPz8/Pz8/Pz8/Pz8/Pz8/Pz8/AT8LIAFBAWohAUGZASEDDOMBCyABQQFqIQFBnAEhAwziAQtBsgEhAyABIARGDfoBIAIoAgAiACAEIAFraiEFIAEgAGtBA2ohBgJAA0AgAS0AACAAQdnPAGotAABHDT0gAEEDRg0BIABBAWohACAEIAFBAWoiAUcNAAsgAiAFNgIADPsBCyACQQA2AgAgBkEBaiEBQSEMPgtBswEhAyABIARGDfkBIAIoAgAiACAEIAFraiEFIAEgAGtBBmohBgJAA0AgAS0AACAAQd3PAGotAABHDTwgAEEGRg0BIABBAWohACAEIAFBAWoiAUcNAAsgAiAFNgIADPoBCyACQQA2AgAgBkEBaiEBQRoMPQsgASAERgRAQbQBIQMM+QELAkACQAJAIAEtAABBxQBrDhEAPT09PT09PT09AT09PT09Aj0LIAFBAWohAUGdASEDDOEBCyABQQFqIQFBngEhAwzgAQsgAUEBaiEBQZ8BIQMM3wELQbUBIQMgASAERg33ASACKAIAIgAgBCABa2ohBSABIABrQQVqIQYCQANAIAEtAAAgAEHkzwBqLQAARw06IABBBUYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAz4AQsgAkEANgIAIAZBAWohAUEoDDsLQbYBIQMgASAERg32ASACKAIAIgAgBCABa2ohBSABIABrQQJqIQYCQANAIAEtAAAgAEHqzwBqLQAARw05IABBAkYNASAAQQFqIQAgBCABQQFqIgFHDQALIAIgBTYCAAz3AQsgAkEANgIAIAZBAWohAUEHDDoLIAEgBEYEQEG3ASEDDPYBCwJAAkAgAS0AAEHFAGsODgA5OTk5OTk5OTk5OTkBOQsgAUEBaiEBQaEBIQMM3QELIAFBAWohAUGiASEDDNwBC0G4ASEDIAEgBEYN9AEgAigCACIAIAQgAWtqIQUgASAAa0ECaiEGAkADQCABLQAAIABB7c8Aai0AAEcNNyAAQQJGDQEgAEEBaiEAIAQgAUEBaiIBRw0ACyACIAU2AgAM9QELIAJBADYCACAGQQFqIQFBEgw4C0G5ASEDIAEgBEYN8wEgAigCACIAIAQgAWtqIQUgASAAa0EBaiEGAkADQCABLQAAIABB8M8Aai0AAEcNNiAAQQFGDQEgAEEBaiEAIAQgAUEBaiIBRw0ACyACIAU2AgAM9AELIAJBADYCACAGQQFqIQFBIAw3C0G6ASEDIAEgBEYN8gEgAigCACIAIAQgAWtqIQUgASAAa0EBaiEGAkADQCABLQAAIABB8s8Aai0AAEcNNSAAQQFGDQEgAEEBaiEAIAQgAUEBaiIBRw0ACyACIAU2AgAM8wELIAJBADYCACAGQQFqIQFBDww2CyABIARGBEBBuwEhAwzyAQsCQAJAIAEtAABByQBrDgcANTU1NTUBNQsgAUEBaiEBQaUBIQMM2QELIAFBAWohAUGmASEDDNgBC0G8ASEDIAEgBEYN8AEgAigCACIAIAQgAWtqIQUgASAAa0EHaiEGAkADQCABLQAAIABB9M8Aai0AAEcNMyAAQQdGDQEgAEEBaiEAIAQgAUEBaiIBRw0ACyACIAU2AgAM8QELIAJBADYCACAGQQFqIQFBGww0CyABIARGBEBBvQEhAwzwAQsCQAJAAkAgAS0AAEHCAGsOEgA0NDQ0NDQ0NDQBNDQ0NDQ0AjQLIAFBAWohAUGkASEDDNgBCyABQQFqIQFBpwEhAwzXAQsgAUEBaiEBQagBIQMM1gELIAEgBEYEQEG+ASEDDO8BCyABLQAAQc4ARw0wIAFBAWohAQwsCyABIARGBEBBvwEhAwzuAQsCQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQCABLQAAQcEAaw4VAAECAz8EBQY/Pz8HCAkKCz8MDQ4PPwsgAUEBaiEBQegAIQMM4wELIAFBAWohAUHpACEDDOIBCyABQQFqIQFB7gAhAwzhAQsgAUEBaiEBQfIAIQMM4AELIAFBAWohAUHzACEDDN8BCyABQQFqIQFB9gAhAwzeAQsgAUEBaiEBQfcAIQMM3QELIAFBAWohAUH6ACEDDNwBCyABQQFqIQFBgwEhAwzbAQsgAUEBaiEBQYQBIQMM2gELIAFBAWohAUGFASEDDNkBCyABQQFqIQFBkgEhAwzYAQsgAUEBaiEBQZgBIQMM1wELIAFBAWohAUGgASEDDNYBCyABQQFqIQFBowEhAwzVAQsgAUEBaiEBQaoBIQMM1AELIAEgBEcEQCACQRA2AgggAiABNgIEQasBIQMM1AELQcABIQMM7AELQQAhAAJAIAIoAjgiA0UNACADKAI0IgNFDQAgAiADEQAAIQALIABFDV4gAEEVRw0HIAJB0QA2AhwgAiABNgIUIAJBsBc2AhAgAkEVNgIMQQAhAwzrAQsgAUEBaiABIARHDQgaQcIBIQMM6gELA0ACQCABLQAAQQprDgQIAAALAAsgBCABQQFqIgFHDQALQcMBIQMM6QELIAEgBEcEQCACQRE2AgggAiABNgIEQQEhAwzQAQtBxAEhAwzoAQsgASAERgRAQcUBIQMM6AELAkACQCABLQAAQQprDgQBKCgAKAsgAUEBagwJCyABQQFqDAULIAEgBEYEQEHGASEDDOcBCwJAAkAgAS0AAEEKaw4XAQsLAQsLCwsLCwsLCwsLCwsLCwsLCwALCyABQQFqIQELQbABIQMMzQELIAEgBEYEQEHIASEDDOYBCyABLQAAQSBHDQkgAkEAOwEyIAFBAWohAUGzASEDDMwBCwNAIAEhAAJAIAEgBEcEQCABLQAAQTBrQf8BcSIDQQpJDQEMJwtBxwEhAwzmAQsCQCACLwEyIgFBmTNLDQAgAiABQQpsIgU7ATIgBUH+/wNxIANB//8Dc0sNACAAQQFqIQEgAiADIAVqIgM7ATIgA0H//wNxQegHSQ0BCwtBACEDIAJBADYCHCACQcEJNgIQIAJBDTYCDCACIABBAWo2AhQM5AELIAJBADYCHCACIAE2AhQgAkHwDDYCECACQRs2AgxBACEDDOMBCyACKAIEIQAgAkEANgIEIAIgACABECYiAA0BIAFBAWoLIQFBrQEhAwzIAQsgAkHBATYCHCACIAA2AgwgAiABQQFqNgIUQQAhAwzgAQsgAigCBCEAIAJBADYCBCACIAAgARAmIgANASABQQFqCyEBQa4BIQMMxQELIAJBwgE2AhwgAiAANgIMIAIgAUEBajYCFEEAIQMM3QELIAJBADYCHCACIAE2AhQgAkGXCzYCECACQQ02AgxBACEDDNwBCyACQQA2AhwgAiABNgIUIAJB4xA2AhAgAkEJNgIMQQAhAwzbAQsgAkECOgAoDKwBC0EAIQMgAkEANgIcIAJBrws2AhAgAkECNgIMIAIgAUEBajYCFAzZAQtBAiEDDL8BC0ENIQMMvgELQSYhAwy9AQtBFSEDDLwBC0EWIQMMuwELQRghAwy6AQtBHCEDDLkBC0EdIQMMuAELQSAhAwy3AQtBISEDDLYBC0EjIQMMtQELQcYAIQMMtAELQS4hAwyzAQtBPSEDDLIBC0HLACEDDLEBC0HOACEDDLABC0HYACEDDK8BC0HZACEDDK4BC0HbACEDDK0BC0HxACEDDKwBC0H0ACEDDKsBC0GNASEDDKoBC0GXASEDDKkBC0GpASEDDKgBC0GvASEDDKcBC0GxASEDDKYBCyACQQA2AgALQQAhAyACQQA2AhwgAiABNgIUIAJB8Rs2AhAgAkEGNgIMDL0BCyACQQA2AgAgBkEBaiEBQSQLOgApIAIoAgQhACACQQA2AgQgAiAAIAEQJyIARQRAQeUAIQMMowELIAJB+QA2AhwgAiABNgIUIAIgADYCDEEAIQMMuwELIABBFUcEQCACQQA2AhwgAiABNgIUIAJBzA42AhAgAkEgNgIMQQAhAwy7AQsgAkH4ADYCHCACIAE2AhQgAkHKGDYCECACQRU2AgxBACEDDLoBCyACQQA2AhwgAiABNgIUIAJBjhs2AhAgAkEGNgIMQQAhAwy5AQsgAkEANgIcIAIgATYCFCACQf4RNgIQIAJBBzYCDEEAIQMMuAELIAJBADYCHCACIAE2AhQgAkGMHDYCECACQQc2AgxBACEDDLcBCyACQQA2AhwgAiABNgIUIAJBww82AhAgAkEHNgIMQQAhAwy2AQsgAkEANgIcIAIgATYCFCACQcMPNgIQIAJBBzYCDEEAIQMMtQELIAIoAgQhACACQQA2AgQgAiAAIAEQJSIARQ0RIAJB5QA2AhwgAiABNgIUIAIgADYCDEEAIQMMtAELIAIoAgQhACACQQA2AgQgAiAAIAEQJSIARQ0gIAJB0wA2AhwgAiABNgIUIAIgADYCDEEAIQMMswELIAIoAgQhACACQQA2AgQgAiAAIAEQJSIARQ0iIAJB0gA2AhwgAiABNgIUIAIgADYCDEEAIQMMsgELIAIoAgQhACACQQA2AgQgAiAAIAEQJSIARQ0OIAJB5QA2AhwgAiABNgIUIAIgADYCDEEAIQMMsQELIAIoAgQhACACQQA2AgQgAiAAIAEQJSIARQ0dIAJB0wA2AhwgAiABNgIUIAIgADYCDEEAIQMMsAELIAIoAgQhACACQQA2AgQgAiAAIAEQJSIARQ0fIAJB0gA2AhwgAiABNgIUIAIgADYCDEEAIQMMrwELIABBP0cNASABQQFqCyEBQQUhAwyUAQtBACEDIAJBADYCHCACIAE2AhQgAkH9EjYCECACQQc2AgwMrAELIAJBADYCHCACIAE2AhQgAkHcCDYCECACQQc2AgxBACEDDKsBCyACKAIEIQAgAkEANgIEIAIgACABECUiAEUNByACQeUANgIcIAIgATYCFCACIAA2AgxBACEDDKoBCyACKAIEIQAgAkEANgIEIAIgACABECUiAEUNFiACQdMANgIcIAIgATYCFCACIAA2AgxBACEDDKkBCyACKAIEIQAgAkEANgIEIAIgACABECUiAEUNGCACQdIANgIcIAIgATYCFCACIAA2AgxBACEDDKgBCyACQQA2AhwgAiABNgIUIAJBxgo2AhAgAkEHNgIMQQAhAwynAQsgAigCBCEAIAJBADYCBCACIAAgARAlIgBFDQMgAkHlADYCHCACIAE2AhQgAiAANgIMQQAhAwymAQsgAigCBCEAIAJBADYCBCACIAAgARAlIgBFDRIgAkHTADYCHCACIAE2AhQgAiAANgIMQQAhAwylAQsgAigCBCEAIAJBADYCBCACIAAgARAlIgBFDRQgAkHSADYCHCACIAE2AhQgAiAANgIMQQAhAwykAQsgAigCBCEAIAJBADYCBCACIAAgARAlIgBFDQAgAkHlADYCHCACIAE2AhQgAiAANgIMQQAhAwyjAQtB1QAhAwyJAQsgAEEVRwRAIAJBADYCHCACIAE2AhQgAkG5DTYCECACQRo2AgxB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AkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAAkACQAJAIABB5ABrDvQDY2IAAWFhYWFhYQIDBAVhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhBgcICQoLDA0OD2FhYWFhEGFhYWFhYWFhYWFhEWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYRITFBUWFxgZGhthYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhHB0eHyAhIiMkJSYnKCkqKywtLi8wMTIzNDU2YTc4OTphYWFhYWFhYTthYWE8YWFhYT0+P2FhYWFhYWFhQGFhQWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYUJDREVGR0hJSktMTU5PUFFSU2FhYWFhYWFhVFVWV1hZWlthXF1hYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFeYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhX2BhC0HhJw8LQaQhDwtByywPC0H+MQ8LQcAkDwtBqyQPC0GNKA8LQeImDwtBgDAPC0G5Lw8LQdckDwtB7x8PC0HhHw8LQfofDwtB8iAPC0GoLw8LQa4yDwtBiDAPC0HsJw8LQYIiDwtBjh0PC0HQLg8LQcojDwtBxTIPC0HfHA8LQdIcDwtBxCAPC0HXIA8LQaIfDwtB7S4PC0GrMA8LQdQlDwtBzC4PC0H6Lg8LQfwrDwtB0jAPC0HxHQ8LQbsgDwtB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AAADAEGUCAsFBAAAAAUAQaQICwkGAAAABwAAAAgAQdwIC4otSW52YWxpZCBjaGFyIGluIHVybCBxdWVyeQBTcGFuIGNhbGxiYWNrIGVycm9yIGluIG9uX2JvZHkAQ29udGVudC1MZW5ndGggb3ZlcmZsb3cAQ2h1bmsgc2l6ZSBvdmVyZmxvdwBSZXNwb25zZSBvdmVyZmxvdwBJbnZhbGlkIG1ldGhvZCBmb3IgSFRUUC94LnggcmVxdWVzdABJbnZhbGlkIG1ldGhvZCBmb3IgUlRTUC94LnggcmVxdWVzdABFeHBlY3RlZCBTT1VSQ0UgbWV0aG9kIGZvciBJQ0UveC54IHJlcXVlc3QASW52YWxpZCBjaGFyIGluIHVybCBmcmFnbWVudCBzdGFydABFeHBlY3RlZCBkb3QAU3BhbiBjYWxsYmFjayBlcnJvciBpbiBvbl9zdGF0dXMASW52YWxpZCByZXNwb25zZSBzdGF0dXMASW52YWxpZCBjaGFyYWN0ZXIgaW4gY2h1bmsgZXh0ZW5zaW9ucwBVc2VyIGNhbGxiYWNrIGVycm9yAGBvbl9yZXNldGAgY2FsbGJhY2sgZXJyb3IAYG9uX2NodW5rX2hlYWRlcmAgY2FsbGJhY2sgZXJyb3IAYG9uX21lc3NhZ2VfYmVnaW5gIGNhbGxiYWNrIGVycm9yAGBvbl9jaHVua19leHRlbnNpb25fdmFsdWVgIGNhbGxiYWNrIGVycm9yAGBvbl9zdGF0dXNfY29tcGxldGVgIGNhbGxiYWNrIGVycm9yAGBvbl92ZXJzaW9uX2NvbXBsZXRlYCBjYWxsYmFjayBlcnJvcgBgb25fdXJsX2NvbXBsZXRlYCBjYWxsYmFjayBlcnJvcgBgb25fY2h1bmtfY29tcGxldGVgIGNhbGxiYWNrIGVycm9yAGBvbl9oZWFkZXJfdmFsdWVfY29tcGxldGVgIGNhbGxiYWNrIGVycm9yAGBvbl9tZXNzYWdlX2NvbXBsZXRlYCBjYWxsYmFjayBlcnJvcgBgb25fbWV0aG9kX2NvbXBsZXRlYCBjYWxsYmFjayBlcnJvcgBgb25faGVhZGVyX2ZpZWxkX2NvbXBsZXRlYCBjYWxsYmFjayBlcnJvcgBgb25fY2h1bmtfZXh0ZW5zaW9uX25hbWVgIGNhbGxiYWNrIGVycm9yAFVuZXhwZWN0ZWQgY2hhciBpbiB1cmwgc2VydmVyAEludmFsaWQgaGVhZGVyIHZhbHVlIGNoYXIASW52YWxpZCBoZWFkZXIgZmllbGQgY2hhcgBTcGFuIGNhbGxiYWNrIGVycm9yIGluIG9uX3ZlcnNpb24ASW52YWxpZCBtaW5vciB2ZXJzaW9uAEludmFsaWQgbWFqb3IgdmVyc2lvbgBFeHBlY3RlZCBzcGFjZSBhZnRlciB2ZXJzaW9uAEV4cGVjdGVkIENSTEYgYWZ0ZXIgdmVyc2lvbgBJbnZhbGlkIEhUVFAgdmVyc2lvbgBJbnZhbGlkIGhlYWRlciB0b2tlbgBTcGFuIGNhbGxiYWNrIGVycm9yIGluIG9uX3VybABJbnZhbGlkIGNoYXJhY3RlcnMgaW4gdXJsAFVuZXhwZWN0ZWQgc3RhcnQgY2hhciBpbiB1cmwARG91YmxlIEAgaW4gdXJsAEVtcHR5IENvbnRlbnQtTGVuZ3RoAEludmFsaWQgY2hhcmFjdGVyIGluIENvbnRlbnQtTGVuZ3RoAER1cGxpY2F0ZSBDb250ZW50LUxlbmd0aABJbnZhbGlkIGNoYXIgaW4gdXJsIHBhdGgAQ29udGVudC1MZW5ndGggY2FuJ3QgYmUgcHJlc2VudCB3aXRoIFRyYW5zZmVyLUVuY29kaW5nAEludmFsaWQgY2hhcmFjdGVyIGluIGNodW5rIHNpemUAU3BhbiBjYWxsYmFjayBlcnJvciBpbiBvbl9oZWFkZXJfdmFsdWUAU3BhbiBjYWxsYmFjayBlcnJvciBpbiBvbl9jaHVua19leHRlbnNpb25fdmFsdWUASW52YWxpZCBjaGFyYWN0ZXIgaW4gY2h1bmsgZXh0ZW5zaW9ucyB2YWx1ZQBNaXNzaW5nIGV4cGVjdGVkIExGIGFmdGVyIGhlYWRlciB2YWx1ZQBJbnZhbGlkIGBUcmFuc2Zlci1FbmNvZGluZ2AgaGVhZGVyIHZhbHVlAEludmFsaWQgY2hhcmFjdGVyIGluIGNodW5rIGV4dGVuc2lvbnMgcXVvdGUgdmFsdWUASW52YWxpZCBjaGFyYWN0ZXIgaW4gY2h1bmsgZXh0ZW5zaW9ucyBxdW90ZWQgdmFsdWUAUGF1c2VkIGJ5IG9uX2hlYWRlcnNfY29tcGxldGUASW52YWxpZCBFT0Ygc3RhdGUAb25fcmVzZXQgcGF1c2UAb25fY2h1bmtfaGVhZGVyIHBhdXNlAG9uX21lc3NhZ2VfYmVnaW4gcGF1c2UAb25fY2h1bmtfZXh0ZW5zaW9uX3ZhbHVlIHBhdXNlAG9uX3N0YXR1c19jb21wbGV0ZSBwYXVzZQBvbl92ZXJzaW9uX2NvbXBsZXRlIHBhdXNlAG9uX3VybF9jb21wbGV0ZSBwYXVzZQBvbl9jaHVua19jb21wbGV0ZSBwYXVzZQBvbl9oZWFkZXJfdmFsdWVfY29tcGxldGUgcGF1c2UAb25fbWVzc2FnZV9jb21wbGV0ZSBwYXVzZQBvbl9tZXRob2RfY29tcGxldGUgcGF1c2UAb25faGVhZGVyX2ZpZWxkX2NvbXBsZXRlIHBhdXNlAG9uX2NodW5rX2V4dGVuc2lvbl9uYW1lIHBhdXNlAFVuZXhwZWN0ZWQgc3BhY2UgYWZ0ZXIgc3RhcnQgbGluZQBTcGFuIGNhbGxiYWNrIGVycm9yIGluIG9uX2NodW5rX2V4dGVuc2lvbl9uYW1lAEludmFsaWQgY2hhcmFjdGVyIGluIGNodW5rIGV4dGVuc2lvbnMgbmFtZQBQYXVzZSBvbiBDT05ORUNUL1VwZ3JhZGUAUGF1c2Ugb24gUFJJL1VwZ3JhZGUARXhwZWN0ZWQgSFRUUC8yIENvbm5lY3Rpb24gUHJlZmFjZQBTcGFuIGNhbGxiYWNrIGVycm9yIGluIG9uX21ldGhvZABFeHBlY3RlZCBzcGFjZSBhZnRlciBtZXRob2QAU3BhbiBjYWxsYmFjayBlcnJvciBpbiBvbl9oZWFkZXJfZmllbGQAUGF1c2VkAEludmFsaWQgd29yZCBlbmNvdW50ZXJlZABJbnZhbGlkIG1ldGhvZCBlbmNvdW50ZXJlZABVbmV4cGVjdGVkIGNoYXIgaW4gdXJsIHNjaGVtYQBSZXF1ZXN0IGhhcyBpbnZhbGlkIGBUcmFuc2Zlci1FbmNvZGluZ2AAU1dJVENIX1BST1hZAFVTRV9QUk9YWQBNS0FDVElWSVRZAFVOUFJPQ0VTU0FCTEVfRU5USVRZAENPUFkATU9WRURfUEVSTUFORU5UTFkAVE9PX0VBUkxZAE5PVElGWQBGQUlMRURfREVQRU5ERU5DWQBCQURfR0FURVdBWQBQTEFZAFBVVABDSEVDS09VVABHQVRFV0FZX1RJTUVPVVQAUkVRVUVTVF9USU1FT1VUAE5FVFdPUktfQ09OTkVDVF9USU1FT1VUAENPTk5FQ1RJT05fVElNRU9VVABMT0dJTl9USU1FT1VUAE5FVFdPUktfUkVBRF9USU1FT1VUAFBPU1QATUlTRElSRUNURURfUkVRVUVTVABDTElFTlRfQ0xPU0VEX1JFUVVFU1QAQ0xJRU5UX0NMT1NFRF9MT0FEX0JBTEFOQ0VEX1JFUVVFU1QAQkFEX1JFUVVFU1QASFRUUF9SRVFVRVNUX1NFTlRfVE9fSFRUUFNfUE9SVABSRVBPUlQASU1fQV9URUFQT1QAUkVTRVRfQ09OVEVOVABOT19DT05URU5UAFBBUlRJQUxfQ09OVEVOVABIUEVfSU5WQUxJRF9DT05TVEFOVABIUEVfQ0JfUkVTRVQAR0VUAEhQRV9TVFJJQ1QAQ09ORkxJQ1QAVEVNUE9SQVJZX1JFRElSRUNUAFBFUk1BTkVOVF9SRURJUkVDVABDT05ORUNUAE1VTFRJX1NUQVRVUwBIUEVfSU5WQUxJRF9TVEFUVVMAVE9PX01BTllfUkVRVUVTVFMARUFSTFlfSElOVFMAVU5BVkFJTEFCTEVfRk9SX0xFR0FMX1JFQVNPTlMAT1BUSU9OUwBTV0lUQ0hJTkdfUFJPVE9DT0xTAFZBUklBTlRfQUxTT19ORUdPVElBVEVTAE1VTFRJUExFX0NIT0lDRVMASU5URVJOQUxfU0VSVkVSX0VSUk9SAFdFQl9TRVJWRVJfVU5LTk9XTl9FUlJPUgBSQUlMR1VOX0VSUk9SAElERU5USVRZX1BST1ZJREVSX0FVVEhFTlRJQ0FUSU9OX0VSUk9SAFNTTF9DRVJUSUZJQ0FURV9FUlJPUgBJTlZBTElEX1hfRk9SV0FSREVEX0ZPUgBTRVRfUEFSQU1FVEVSAEdFVF9QQVJBTUVURVIASFBFX1VTRVIAU0VFX09USEVSAEhQRV9DQl9DSFVOS19IRUFERVIATUtDQUxFTkRBUgBTRVRVUABXRUJfU0VSVkVSX0lTX0RPV04AVEVBUkRPV04ASFBFX0NMT1NFRF9DT05ORUNUSU9OAEhFVVJJU1RJQ19FWFBJUkFUSU9OAERJU0NPTk5FQ1RFRF9PUEVSQVRJT04ATk9OX0FVVEhPUklUQVRJVkVfSU5GT1JNQVRJT04ASFBFX0lOVkFMSURfVkVSU0lPTgBIUEVfQ0JfTUVTU0FHRV9CRUdJTgBTSVRFX0lTX0ZST1pFTgBIUEVfSU5WQUxJRF9IRUFERVJfVE9LRU4ASU5WQUxJRF9UT0tFTgBGT1JCSURERU4ARU5IQU5DRV9ZT1VSX0NBTE0ASFBFX0lOVkFMSURfVVJMAEJMT0NLRURfQllfUEFSRU5UQUxfQ09OVFJPTABNS0NPTABBQ0wASFBFX0lOVEVSTkFMAFJFUVVFU1RfSEVBREVSX0ZJRUxEU19UT09fTEFSR0VfVU5PRkZJQ0lBTABIUEVfT0sAVU5MSU5LAFVOTE9DSwBQUkkAUkVUUllfV0lUSABIUEVfSU5WQUxJRF9DT05URU5UX0xFTkdUSABIUEVfVU5FWFBFQ1RFRF9DT05URU5UX0xFTkdUSABGTFVTSABQUk9QUEFUQ0gATS1TRUFSQ0gAVVJJX1RPT19MT05HAFBST0NFU1NJTkcATUlTQ0VMTEFORU9VU19QRVJTSVNURU5UX1dBUk5JTkcATUlTQ0VMTEFORU9VU19XQVJOSU5HAEhQRV9JTlZBTElEX1RSQU5TRkVSX0VOQ09ESU5HAEV4cGVjdGVkIENSTEYASFBFX0lOVkFMSURfQ0hVTktfU0laRQBNT1ZFAENPTlRJTlVFAEhQRV9DQl9TVEFUVVNfQ09NUExFVEUASFBFX0NCX0hFQURFUlNfQ09NUExFVEUASFBFX0NCX1ZFUlNJT05fQ09NUExFVEUASFBFX0NCX1VSTF9DT01QTEVURQBIUEVfQ0JfQ0hVTktfQ09NUExFVEUASFBFX0NCX0hFQURFUl9WQUxVRV9DT01QTEVURQBIUEVfQ0JfQ0hVTktfRVhURU5TSU9OX1ZBTFVFX0NPTVBMRVRFAEhQRV9DQl9DSFVOS19FWFRFTlNJT05fTkFNRV9DT01QTEVURQBIUEVfQ0JfTUVTU0FHRV9DT01QTEVURQBIUEVfQ0JfTUVUSE9EX0NPTVBMRVRFAEhQRV9DQl9IRUFERVJfRklFTERfQ09NUExFVEUAREVMRVRFAEhQRV9JTlZBTElEX0VPRl9TVEFURQBJTlZBTElEX1NTTF9DRVJUSUZJQ0FURQBQQVVTRQBOT19SRVNQT05TRQBVTlNVUFBPUlRFRF9NRURJQV9UWVBFAEdPTkUATk9UX0FDQ0VQVEFCTEUAU0VSVklDRV9VTkFWQUlMQUJMRQBSQU5HRV9OT1RfU0FUSVNGSUFCTEUAT1JJR0lOX0lTX1VOUkVBQ0hBQkxFAFJFU1BPTlNFX0lTX1NUQUxFAFBVUkdFAE1FUkdFAFJFUVVFU1RfSEVBREVSX0ZJRUxEU19UT09fTEFSR0UAUkVRVUVTVF9IRUFERVJfVE9PX0xBUkdFAFBBWUxPQURfVE9PX0xBUkdFAElOU1VGRklDSUVOVF9TVE9SQUdFAEhQRV9QQVVTRURfVVBHUkFERQBIUEVfUEFVU0VEX0gyX1VQR1JBREUAU09VUkNFAEFOTk9VTkNFAFRSQUNFAEhQRV9VTkVYUEVDVEVEX1NQQUNFAERFU0NSSUJFAFVOU1VCU0NSSUJFAFJFQ09SRABIUEVfSU5WQUxJRF9NRVRIT0QATk9UX0ZPVU5EAFBST1BGSU5EAFVOQklORABSRUJJTkQAVU5BVVRIT1JJWkVEAE1FVEhPRF9OT1RfQUxMT1dFRABIVFRQX1ZFUlNJT05fTk9UX1NVUFBPUlRFRABBTFJFQURZX1JFUE9SVEVEAEFDQ0VQVEVEAE5PVF9JTVBMRU1FTlRFRABMT09QX0RFVEVDVEVEAEhQRV9DUl9FWFBFQ1RFRABIUEVfTEZfRVhQRUNURUQAQ1JFQVRFRABJTV9VU0VEAEhQRV9QQVVTRUQAVElNRU9VVF9PQ0NVUkVEAFBBWU1FTlRfUkVRVUlSRUQAUFJFQ09ORElUSU9OX1JFUVVJUkVEAFBST1hZX0FVVEhFTlRJQ0FUSU9OX1JFUVVJUkVEAE5FVFdPUktfQVVUSEVOVElDQVRJT05fUkVRVUlSRUQATEVOR1RIX1JFUVVJUkVEAFNTTF9DRVJUSUZJQ0FURV9SRVFVSVJFRABVUEdSQURFX1JFUVVJUkVEAFBBR0VfRVhQSVJFRABQUkVDT05ESVRJT05fRkFJTEVEAEVYUEVDVEFUSU9OX0ZBSUxFRABSRVZBTElEQVRJT05fRkFJTEVEAFNTTF9IQU5EU0hBS0VfRkFJTEVEAExPQ0tFRABUUkFOU0ZPUk1BVElPTl9BUFBMSUVEAE5PVF9NT0RJRklFRABOT1RfRVhURU5ERUQAQkFORFdJRFRIX0xJTUlUX0VYQ0VFREVEAFNJVEVfSVNfT1ZFUkxPQURFRABIRUFEAEV4cGVjdGVkIEhUVFAvAABeEwAAJhMAADAQAADwFwAAnRMAABUSAAA5FwAA8BIAAAoQAAB1EgAArRIAAIITAABPFAAAfxAAAKAVAAAjFAAAiRIAAIsUAABNFQAA1BEAAM8UAAAQGAAAyRYAANwWAADBEQAA4BcAALsUAAB0FAAAfBUAAOUUAAAIFwAAHxAAAGUVAACjFAAAKBUAAAIVAACZFQAALBAAAIsZAABPDwAA1A4AAGoQAADOEAAAAhcAAIkOAABuEwAAHBMAAGYUAABWFwAAwRMAAM0TAABsEwAAaBcAAGYXAABfFwAAIhMAAM4PAABpDgAA2A4AAGMWAADLEwAAqg4AACgXAAAmFwAAxRMAAF0WAADoEQAAZxMAAGUTAADyFgAAcxMAAB0XAAD5FgAA8xEAAM8OAADOFQAADBIAALMRAAClEQAAYRAAADIXAAC7EwBB+TULAQEAQZA2C+ABAQECAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEAQf03CwEBAEGROAteAgMCAgICAgAAAgIAAgIAAgICAgICAgICAgAEAAAAAAACAgICAgICAgICAgICAgICAgICAgICAgICAgAAAAICAgICAgICAgICAgICAgICAgICAgICAgICAgICAAIAAgBB/TkLAQEAQZE6C14CAAICAgICAAACAgACAgACAgICAgICAgICAAMABAAAAAICAgICAgICAgICAgICAgICAgICAgICAgICAAAAAgICAgICAgICAgICAgICAgICAgICAgICAgICAgIAAgACAEHwOwsNbG9zZWVlcC1hbGl2ZQBBiTwLAQEAQaA8C+ABAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEAAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEAQYk+CwEBAEGgPgvnAQEBAQEBAQEBAQEBAQIBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBY2h1bmtlZABBsMAAC18BAQABAQEBAQAAAQEAAQEAAQEBAQEBAQEBAQAAAAAAAAABAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQAAAAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAAEAAQBBkMIACyFlY3Rpb25lbnQtbGVuZ3Rob25yb3h5LWNvbm5lY3Rpb24AQcDCAAstcmFuc2Zlci1lbmNvZGluZ3BncmFkZQ0KDQoNClNNDQoNClRUUC9DRS9UU1AvAEH5wgALBQECAAEDAEGQwwAL4AEEAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQABAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQBB+cQACwUBAgABAwBBkMUAC+ABBAEBBQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEAAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEAQfnGAAsEAQAAAQBBkccAC98BAQEAAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQABAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQBB+sgACwQBAAACAEGQyQALXwMEAAAEBAQEBAQEBAQEBAUEBAQEBAQEBAQEBAQABAAGBwQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAAEAAQABAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQAAAAEAEH6ygALBAEAAAEAQZDLAAsBAQBBqssAC0ECAAAAAAAAAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMAAAAAAAADAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwBB+swACwQBAAABAEGQzQALAQEAQZrNAAsGAgAAAAACAEGxzQALOgMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAAAAAAAAAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMAQfDOAAuWAU5PVU5DRUVDS09VVE5FQ1RFVEVDUklCRUxVU0hFVEVBRFNFQVJDSFJHRUNUSVZJVFlMRU5EQVJWRU9USUZZUFRJT05TQ0hTRUFZU1RBVENIR0VPUkRJUkVDVE9SVFJDSFBBUkFNRVRFUlVSQ0VCU0NSSUJFQVJET1dOQUNFSU5ETktDS1VCU0NSSUJFSFRUUC9BRFRQLw==", "base64");
});

// node_modules/undici/lib/web/fetch/constants.js
var require_constants3 = __commonJS((exports, module) => {
  var corsSafeListedMethods = ["GET", "HEAD", "POST"];
  var corsSafeListedMethodsSet = new Set(corsSafeListedMethods);
  var nullBodyStatus = [101, 204, 205, 304];
  var redirectStatus = [301, 302, 303, 307, 308];
  var redirectStatusSet = new Set(redirectStatus);
  var badPorts = [
    "1",
    "7",
    "9",
    "11",
    "13",
    "15",
    "17",
    "19",
    "20",
    "21",
    "22",
    "23",
    "25",
    "37",
    "42",
    "43",
    "53",
    "69",
    "77",
    "79",
    "87",
    "95",
    "101",
    "102",
    "103",
    "104",
    "109",
    "110",
    "111",
    "113",
    "115",
    "117",
    "119",
    "123",
    "135",
    "137",
    "139",
    "143",
    "161",
    "179",
    "389",
    "427",
    "465",
    "512",
    "513",
    "514",
    "515",
    "526",
    "530",
    "531",
    "532",
    "540",
    "548",
    "554",
    "556",
    "563",
    "587",
    "601",
    "636",
    "989",
    "990",
    "993",
    "995",
    "1719",
    "1720",
    "1723",
    "2049",
    "3659",
    "4045",
    "4190",
    "5060",
    "5061",
    "6000",
    "6566",
    "6665",
    "6666",
    "6667",
    "6668",
    "6669",
    "6679",
    "6697",
    "10080"
  ];
  var badPortsSet = new Set(badPorts);
  var referrerPolicy = [
    "",
    "no-referrer",
    "no-referrer-when-downgrade",
    "same-origin",
    "origin",
    "strict-origin",
    "origin-when-cross-origin",
    "strict-origin-when-cross-origin",
    "unsafe-url"
  ];
  var referrerPolicySet = new Set(referrerPolicy);
  var requestRedirect = ["follow", "manual", "error"];
  var safeMethods = ["GET", "HEAD", "OPTIONS", "TRACE"];
  var safeMethodsSet = new Set(safeMethods);
  var requestMode = ["navigate", "same-origin", "no-cors", "cors"];
  var requestCredentials = ["omit", "same-origin", "include"];
  var requestCache = [
    "default",
    "no-store",
    "reload",
    "no-cache",
    "force-cache",
    "only-if-cached"
  ];
  var requestBodyHeader = [
    "content-encoding",
    "content-language",
    "content-location",
    "content-type",
    "content-length"
  ];
  var requestDuplex = [
    "half"
  ];
  var forbiddenMethods = ["CONNECT", "TRACE", "TRACK"];
  var forbiddenMethodsSet = new Set(forbiddenMethods);
  var subresource = [
    "audio",
    "audioworklet",
    "font",
    "image",
    "manifest",
    "paintworklet",
    "script",
    "style",
    "track",
    "video",
    "xslt",
    ""
  ];
  var subresourceSet = new Set(subresource);
  module.exports = {
    subresource,
    forbiddenMethods,
    requestBodyHeader,
    referrerPolicy,
    requestRedirect,
    requestMode,
    requestCredentials,
    requestCache,
    redirectStatus,
    corsSafeListedMethods,
    nullBodyStatus,
    safeMethods,
    badPorts,
    requestDuplex,
    subresourceSet,
    badPortsSet,
    redirectStatusSet,
    corsSafeListedMethodsSet,
    safeMethodsSet,
    forbiddenMethodsSet,
    referrerPolicySet
  };
});

// node_modules/undici/lib/web/fetch/global.js
var require_global = __commonJS((exports, module) => {
  var globalOrigin = Symbol.for("undici.globalOrigin.1");
  function getGlobalOrigin() {
    return globalThis[globalOrigin];
  }
  function setGlobalOrigin(newOrigin) {
    if (newOrigin === undefined) {
      Object.defineProperty(globalThis, globalOrigin, {
        value: undefined,
        writable: true,
        enumerable: false,
        configurable: false
      });
      return;
    }
    const parsedURL = new URL(newOrigin);
    if (parsedURL.protocol !== "http:" && parsedURL.protocol !== "https:") {
      throw new TypeError(`Only http & https urls are allowed, received ${parsedURL.protocol}`);
    }
    Object.defineProperty(globalThis, globalOrigin, {
      value: parsedURL,
      writable: true,
      enumerable: false,
      configurable: false
    });
  }
  module.exports = {
    getGlobalOrigin,
    setGlobalOrigin
  };
});

// node_modules/undici/lib/web/fetch/data-url.js
var require_data_url = __commonJS((exports, module) => {
  var assert = __require("assert");
  var encoder = new TextEncoder;
  var HTTP_TOKEN_CODEPOINTS = /^[!#$%&'*+\-.^_|~A-Za-z0-9]+$/;
  var HTTP_WHITESPACE_REGEX = /[\u000A\u000D\u0009\u0020]/;
  var ASCII_WHITESPACE_REPLACE_REGEX = /[\u0009\u000A\u000C\u000D\u0020]/g;
  var HTTP_QUOTED_STRING_TOKENS = /^[\u0009\u0020-\u007E\u0080-\u00FF]+$/;
  function dataURLProcessor(dataURL) {
    assert(dataURL.protocol === "data:");
    let input = URLSerializer(dataURL, true);
    input = input.slice(5);
    const position = { position: 0 };
    let mimeType = collectASequenceOfCodePointsFast(",", input, position);
    const mimeTypeLength = mimeType.length;
    mimeType = removeASCIIWhitespace(mimeType, true, true);
    if (position.position >= input.length) {
      return "failure";
    }
    position.position++;
    const encodedBody = input.slice(mimeTypeLength + 1);
    let body = stringPercentDecode(encodedBody);
    if (/;(\u0020){0,}base64$/i.test(mimeType)) {
      const stringBody = isomorphicDecode(body);
      body = forgivingBase64(stringBody);
      if (body === "failure") {
        return "failure";
      }
      mimeType = mimeType.slice(0, -6);
      mimeType = mimeType.replace(/(\u0020)+$/, "");
      mimeType = mimeType.slice(0, -1);
    }
    if (mimeType.startsWith(";")) {
      mimeType = "text/plain" + mimeType;
    }
    let mimeTypeRecord = parseMIMEType(mimeType);
    if (mimeTypeRecord === "failure") {
      mimeTypeRecord = parseMIMEType("text/plain;charset=US-ASCII");
    }
    return { mimeType: mimeTypeRecord, body };
  }
  function URLSerializer(url, excludeFragment = false) {
    if (!excludeFragment) {
      return url.href;
    }
    const href = url.href;
    const hashLength = url.hash.length;
    const serialized = hashLength === 0 ? href : href.substring(0, href.length - hashLength);
    if (!hashLength && href.endsWith("#")) {
      return serialized.slice(0, -1);
    }
    return serialized;
  }
  function collectASequenceOfCodePoints(condition, input, position) {
    let result = "";
    while (position.position < input.length && condition(input[position.position])) {
      result += input[position.position];
      position.position++;
    }
    return result;
  }
  function collectASequenceOfCodePointsFast(char, input, position) {
    const idx = input.indexOf(char, position.position);
    const start = position.position;
    if (idx === -1) {
      position.position = input.length;
      return input.slice(start);
    }
    position.position = idx;
    return input.slice(start, position.position);
  }
  function stringPercentDecode(input) {
    const bytes = encoder.encode(input);
    return percentDecode(bytes);
  }
  function isHexCharByte(byte) {
    return byte >= 48 && byte <= 57 || byte >= 65 && byte <= 70 || byte >= 97 && byte <= 102;
  }
  function hexByteToNumber(byte) {
    return byte >= 48 && byte <= 57 ? byte - 48 : (byte & 223) - 55;
  }
  function percentDecode(input) {
    const length = input.length;
    const output = new Uint8Array(length);
    let j = 0;
    for (let i = 0;i < length; ++i) {
      const byte = input[i];
      if (byte !== 37) {
        output[j++] = byte;
      } else if (byte === 37 && !(isHexCharByte(input[i + 1]) && isHexCharByte(input[i + 2]))) {
        output[j++] = 37;
      } else {
        output[j++] = hexByteToNumber(input[i + 1]) << 4 | hexByteToNumber(input[i + 2]);
        i += 2;
      }
    }
    return length === j ? output : output.subarray(0, j);
  }
  function parseMIMEType(input) {
    input = removeHTTPWhitespace(input, true, true);
    const position = { position: 0 };
    const type = collectASequenceOfCodePointsFast("/", input, position);
    if (type.length === 0 || !HTTP_TOKEN_CODEPOINTS.test(type)) {
      return "failure";
    }
    if (position.position > input.length) {
      return "failure";
    }
    position.position++;
    let subtype = collectASequenceOfCodePointsFast(";", input, position);
    subtype = removeHTTPWhitespace(subtype, false, true);
    if (subtype.length === 0 || !HTTP_TOKEN_CODEPOINTS.test(subtype)) {
      return "failure";
    }
    const typeLowercase = type.toLowerCase();
    const subtypeLowercase = subtype.toLowerCase();
    const mimeType = {
      type: typeLowercase,
      subtype: subtypeLowercase,
      parameters: new Map,
      essence: `${typeLowercase}/${subtypeLowercase}`
    };
    while (position.position < input.length) {
      position.position++;
      collectASequenceOfCodePoints((char) => HTTP_WHITESPACE_REGEX.test(char), input, position);
      let parameterName = collectASequenceOfCodePoints((char) => char !== ";" && char !== "=", input, position);
      parameterName = parameterName.toLowerCase();
      if (position.position < input.length) {
        if (input[position.position] === ";") {
          continue;
        }
        position.position++;
      }
      if (position.position > input.length) {
        break;
      }
      let parameterValue = null;
      if (input[position.position] === '"') {
        parameterValue = collectAnHTTPQuotedString(input, position, true);
        collectASequenceOfCodePointsFast(";", input, position);
      } else {
        parameterValue = collectASequenceOfCodePointsFast(";", input, position);
        parameterValue = removeHTTPWhitespace(parameterValue, false, true);
        if (parameterValue.length === 0) {
          continue;
        }
      }
      if (parameterName.length !== 0 && HTTP_TOKEN_CODEPOINTS.test(parameterName) && (parameterValue.length === 0 || HTTP_QUOTED_STRING_TOKENS.test(parameterValue)) && !mimeType.parameters.has(parameterName)) {
        mimeType.parameters.set(parameterName, parameterValue);
      }
    }
    return mimeType;
  }
  function forgivingBase64(data) {
    data = data.replace(ASCII_WHITESPACE_REPLACE_REGEX, "");
    let dataLength = data.length;
    if (dataLength % 4 === 0) {
      if (data.charCodeAt(dataLength - 1) === 61) {
        --dataLength;
        if (data.charCodeAt(dataLength - 1) === 61) {
          --dataLength;
        }
      }
    }
    if (dataLength % 4 === 1) {
      return "failure";
    }
    if (/[^+/0-9A-Za-z]/.test(data.length === dataLength ? data : data.substring(0, dataLength))) {
      return "failure";
    }
    const buffer = Buffer.from(data, "base64");
    return new Uint8Array(buffer.buffer, buffer.byteOffset, buffer.byteLength);
  }
  function collectAnHTTPQuotedString(input, position, extractValue) {
    const positionStart = position.position;
    let value = "";
    assert(input[position.position] === '"');
    position.position++;
    while (true) {
      value += collectASequenceOfCodePoints((char) => char !== '"' && char !== "\\", input, position);
      if (position.position >= input.length) {
        break;
      }
      const quoteOrBackslash = input[position.position];
      position.position++;
      if (quoteOrBackslash === "\\") {
        if (position.position >= input.length) {
          value += "\\";
          break;
        }
        value += input[position.position];
        position.position++;
      } else {
        assert(quoteOrBackslash === '"');
        break;
      }
    }
    if (extractValue) {
      return value;
    }
    return input.slice(positionStart, position.position);
  }
  function serializeAMimeType(mimeType) {
    assert(mimeType !== "failure");
    const { parameters, essence } = mimeType;
    let serialization = essence;
    for (let [name, value] of parameters.entries()) {
      serialization += ";";
      serialization += name;
      serialization += "=";
      if (!HTTP_TOKEN_CODEPOINTS.test(value)) {
        value = value.replace(/(\\|")/g, "\\$1");
        value = '"' + value;
        value += '"';
      }
      serialization += value;
    }
    return serialization;
  }
  function isHTTPWhiteSpace(char) {
    return char === 13 || char === 10 || char === 9 || char === 32;
  }
  function removeHTTPWhitespace(str, leading = true, trailing = true) {
    return removeChars(str, leading, trailing, isHTTPWhiteSpace);
  }
  function isASCIIWhitespace(char) {
    return char === 13 || char === 10 || char === 9 || char === 12 || char === 32;
  }
  function removeASCIIWhitespace(str, leading = true, trailing = true) {
    return removeChars(str, leading, trailing, isASCIIWhitespace);
  }
  function removeChars(str, leading, trailing, predicate) {
    let lead = 0;
    let trail = str.length - 1;
    if (leading) {
      while (lead < str.length && predicate(str.charCodeAt(lead)))
        lead++;
    }
    if (trailing) {
      while (trail > 0 && predicate(str.charCodeAt(trail)))
        trail--;
    }
    return lead === 0 && trail === str.length - 1 ? str : str.slice(lead, trail + 1);
  }
  function isomorphicDecode(input) {
    const length = input.length;
    if ((2 << 15) - 1 > length) {
      return String.fromCharCode.apply(null, input);
    }
    let result = "";
    let i = 0;
    let addition = (2 << 15) - 1;
    while (i < length) {
      if (i + addition > length) {
        addition = length - i;
      }
      result += String.fromCharCode.apply(null, input.subarray(i, i += addition));
    }
    return result;
  }
  function minimizeSupportedMimeType(mimeType) {
    switch (mimeType.essence) {
      case "application/ecmascript":
      case "application/javascript":
      case "application/x-ecmascript":
      case "application/x-javascript":
      case "text/ecmascript":
      case "text/javascript":
      case "text/javascript1.0":
      case "text/javascript1.1":
      case "text/javascript1.2":
      case "text/javascript1.3":
      case "text/javascript1.4":
      case "text/javascript1.5":
      case "text/jscript":
      case "text/livescript":
      case "text/x-ecmascript":
      case "text/x-javascript":
        return "text/javascript";
      case "application/json":
      case "text/json":
        return "application/json";
      case "image/svg+xml":
        return "image/svg+xml";
      case "text/xml":
      case "application/xml":
        return "application/xml";
    }
    if (mimeType.subtype.endsWith("+json")) {
      return "application/json";
    }
    if (mimeType.subtype.endsWith("+xml")) {
      return "application/xml";
    }
    return "";
  }
  module.exports = {
    dataURLProcessor,
    URLSerializer,
    collectASequenceOfCodePoints,
    collectASequenceOfCodePointsFast,
    stringPercentDecode,
    parseMIMEType,
    collectAnHTTPQuotedString,
    serializeAMimeType,
    removeChars,
    removeHTTPWhitespace,
    minimizeSupportedMimeType,
    HTTP_TOKEN_CODEPOINTS,
    isomorphicDecode
  };
});

// node_modules/undici/lib/web/fetch/webidl.js
var require_webidl = __commonJS((exports, module) => {
  var { types, inspect } = __require("util");
  var { markAsUncloneable } = __require("worker_threads");
  var { toUSVString } = require_util();
  var webidl = {};
  webidl.converters = {};
  webidl.util = {};
  webidl.errors = {};
  webidl.errors.exception = function(message) {
    return new TypeError(`${message.header}: ${message.message}`);
  };
  webidl.errors.conversionFailed = function(context) {
    const plural = context.types.length === 1 ? "" : " one of";
    const message = `${context.argument} could not be converted to` + `${plural}: ${context.types.join(", ")}.`;
    return webidl.errors.exception({
      header: context.prefix,
      message
    });
  };
  webidl.errors.invalidArgument = function(context) {
    return webidl.errors.exception({
      header: context.prefix,
      message: `"${context.value}" is an invalid ${context.type}.`
    });
  };
  webidl.brandCheck = function(V, I, opts) {
    if (opts?.strict !== false) {
      if (!(V instanceof I)) {
        const err = new TypeError("Illegal invocation");
        err.code = "ERR_INVALID_THIS";
        throw err;
      }
    } else {
      if (V?.[Symbol.toStringTag] !== I.prototype[Symbol.toStringTag]) {
        const err = new TypeError("Illegal invocation");
        err.code = "ERR_INVALID_THIS";
        throw err;
      }
    }
  };
  webidl.argumentLengthCheck = function({ length }, min, ctx) {
    if (length < min) {
      throw webidl.errors.exception({
        message: `${min} argument${min !== 1 ? "s" : ""} required, ` + `but${length ? " only" : ""} ${length} found.`,
        header: ctx
      });
    }
  };
  webidl.illegalConstructor = function() {
    throw webidl.errors.exception({
      header: "TypeError",
      message: "Illegal constructor"
    });
  };
  webidl.util.Type = function(V) {
    switch (typeof V) {
      case "undefined":
        return "Undefined";
      case "boolean":
        return "Boolean";
      case "string":
        return "String";
      case "symbol":
        return "Symbol";
      case "number":
        return "Number";
      case "bigint":
        return "BigInt";
      case "function":
      case "object": {
        if (V === null) {
          return "Null";
        }
        return "Object";
      }
    }
  };
  webidl.util.markAsUncloneable = markAsUncloneable || (() => {});
  webidl.util.ConvertToInt = function(V, bitLength, signedness, opts) {
    let upperBound;
    let lowerBound;
    if (bitLength === 64) {
      upperBound = Math.pow(2, 53) - 1;
      if (signedness === "unsigned") {
        lowerBound = 0;
      } else {
        lowerBound = Math.pow(-2, 53) + 1;
      }
    } else if (signedness === "unsigned") {
      lowerBound = 0;
      upperBound = Math.pow(2, bitLength) - 1;
    } else {
      lowerBound = Math.pow(-2, bitLength) - 1;
      upperBound = Math.pow(2, bitLength - 1) - 1;
    }
    let x = Number(V);
    if (x === 0) {
      x = 0;
    }
    if (opts?.enforceRange === true) {
      if (Number.isNaN(x) || x === Number.POSITIVE_INFINITY || x === Number.NEGATIVE_INFINITY) {
        throw webidl.errors.exception({
          header: "Integer conversion",
          message: `Could not convert ${webidl.util.Stringify(V)} to an integer.`
        });
      }
      x = webidl.util.IntegerPart(x);
      if (x < lowerBound || x > upperBound) {
        throw webidl.errors.exception({
          header: "Integer conversion",
          message: `Value must be between ${lowerBound}-${upperBound}, got ${x}.`
        });
      }
      return x;
    }
    if (!Number.isNaN(x) && opts?.clamp === true) {
      x = Math.min(Math.max(x, lowerBound), upperBound);
      if (Math.floor(x) % 2 === 0) {
        x = Math.floor(x);
      } else {
        x = Math.ceil(x);
      }
      return x;
    }
    if (Number.isNaN(x) || x === 0 && Object.is(0, x) || x === Number.POSITIVE_INFINITY || x === Number.NEGATIVE_INFINITY) {
      return 0;
    }
    x = webidl.util.IntegerPart(x);
    x = x % Math.pow(2, bitLength);
    if (signedness === "signed" && x >= Math.pow(2, bitLength) - 1) {
      return x - Math.pow(2, bitLength);
    }
    return x;
  };
  webidl.util.IntegerPart = function(n) {
    const r = Math.floor(Math.abs(n));
    if (n < 0) {
      return -1 * r;
    }
    return r;
  };
  webidl.util.Stringify = function(V) {
    const type = webidl.util.Type(V);
    switch (type) {
      case "Symbol":
        return `Symbol(${V.description})`;
      case "Object":
        return inspect(V);
      case "String":
        return `"${V}"`;
      default:
        return `${V}`;
    }
  };
  webidl.sequenceConverter = function(converter) {
    return (V, prefix, argument, Iterable) => {
      if (webidl.util.Type(V) !== "Object") {
        throw webidl.errors.exception({
          header: prefix,
          message: `${argument} (${webidl.util.Stringify(V)}) is not iterable.`
        });
      }
      const method = typeof Iterable === "function" ? Iterable() : V?.[Symbol.iterator]?.();
      const seq = [];
      let index = 0;
      if (method === undefined || typeof method.next !== "function") {
        throw webidl.errors.exception({
          header: prefix,
          message: `${argument} is not iterable.`
        });
      }
      while (true) {
        const { done, value } = method.next();
        if (done) {
          break;
        }
        seq.push(converter(value, prefix, `${argument}[${index++}]`));
      }
      return seq;
    };
  };
  webidl.recordConverter = function(keyConverter, valueConverter) {
    return (O, prefix, argument) => {
      if (webidl.util.Type(O) !== "Object") {
        throw webidl.errors.exception({
          header: prefix,
          message: `${argument} ("${webidl.util.Type(O)}") is not an Object.`
        });
      }
      const result = {};
      if (!types.isProxy(O)) {
        const keys2 = [...Object.getOwnPropertyNames(O), ...Object.getOwnPropertySymbols(O)];
        for (const key of keys2) {
          const typedKey = keyConverter(key, prefix, argument);
          const typedValue = valueConverter(O[key], prefix, argument);
          result[typedKey] = typedValue;
        }
        return result;
      }
      const keys = Reflect.ownKeys(O);
      for (const key of keys) {
        const desc = Reflect.getOwnPropertyDescriptor(O, key);
        if (desc?.enumerable) {
          const typedKey = keyConverter(key, prefix, argument);
          const typedValue = valueConverter(O[key], prefix, argument);
          result[typedKey] = typedValue;
        }
      }
      return result;
    };
  };
  webidl.interfaceConverter = function(i) {
    return (V, prefix, argument, opts) => {
      if (opts?.strict !== false && !(V instanceof i)) {
        throw webidl.errors.exception({
          header: prefix,
          message: `Expected ${argument} ("${webidl.util.Stringify(V)}") to be an instance of ${i.name}.`
        });
      }
      return V;
    };
  };
  webidl.dictionaryConverter = function(converters) {
    return (dictionary, prefix, argument) => {
      const type = webidl.util.Type(dictionary);
      const dict = {};
      if (type === "Null" || type === "Undefined") {
        return dict;
      } else if (type !== "Object") {
        throw webidl.errors.exception({
          header: prefix,
          message: `Expected ${dictionary} to be one of: Null, Undefined, Object.`
        });
      }
      for (const options of converters) {
        const { key, defaultValue, required, converter } = options;
        if (required === true) {
          if (!Object.hasOwn(dictionary, key)) {
            throw webidl.errors.exception({
              header: prefix,
              message: `Missing required key "${key}".`
            });
          }
        }
        let value = dictionary[key];
        const hasDefault = Object.hasOwn(options, "defaultValue");
        if (hasDefault && value !== null) {
          value ??= defaultValue();
        }
        if (required || hasDefault || value !== undefined) {
          value = converter(value, prefix, `${argument}.${key}`);
          if (options.allowedValues && !options.allowedValues.includes(value)) {
            throw webidl.errors.exception({
              header: prefix,
              message: `${value} is not an accepted type. Expected one of ${options.allowedValues.join(", ")}.`
            });
          }
          dict[key] = value;
        }
      }
      return dict;
    };
  };
  webidl.nullableConverter = function(converter) {
    return (V, prefix, argument) => {
      if (V === null) {
        return V;
      }
      return converter(V, prefix, argument);
    };
  };
  webidl.converters.DOMString = function(V, prefix, argument, opts) {
    if (V === null && opts?.legacyNullToEmptyString) {
      return "";
    }
    if (typeof V === "symbol") {
      throw webidl.errors.exception({
        header: prefix,
        message: `${argument} is a symbol, which cannot be converted to a DOMString.`
      });
    }
    return String(V);
  };
  webidl.converters.ByteString = function(V, prefix, argument) {
    const x = webidl.converters.DOMString(V, prefix, argument);
    for (let index = 0;index < x.length; index++) {
      if (x.charCodeAt(index) > 255) {
        throw new TypeError("Cannot convert argument to a ByteString because the character at " + `index ${index} has a value of ${x.charCodeAt(index)} which is greater than 255.`);
      }
    }
    return x;
  };
  webidl.converters.USVString = toUSVString;
  webidl.converters.boolean = function(V) {
    const x = Boolean(V);
    return x;
  };
  webidl.converters.any = function(V) {
    return V;
  };
  webidl.converters["long long"] = function(V, prefix, argument) {
    const x = webidl.util.ConvertToInt(V, 64, "signed", undefined, prefix, argument);
    return x;
  };
  webidl.converters["unsigned long long"] = function(V, prefix, argument) {
    const x = webidl.util.ConvertToInt(V, 64, "unsigned", undefined, prefix, argument);
    return x;
  };
  webidl.converters["unsigned long"] = function(V, prefix, argument) {
    const x = webidl.util.ConvertToInt(V, 32, "unsigned", undefined, prefix, argument);
    return x;
  };
  webidl.converters["unsigned short"] = function(V, prefix, argument, opts) {
    const x = webidl.util.ConvertToInt(V, 16, "unsigned", opts, prefix, argument);
    return x;
  };
  webidl.converters.ArrayBuffer = function(V, prefix, argument, opts) {
    if (webidl.util.Type(V) !== "Object" || !types.isAnyArrayBuffer(V)) {
      throw webidl.errors.conversionFailed({
        prefix,
        argument: `${argument} ("${webidl.util.Stringify(V)}")`,
        types: ["ArrayBuffer"]
      });
    }
    if (opts?.allowShared === false && types.isSharedArrayBuffer(V)) {
      throw webidl.errors.exception({
        header: "ArrayBuffer",
        message: "SharedArrayBuffer is not allowed."
      });
    }
    if (V.resizable || V.growable) {
      throw webidl.errors.exception({
        header: "ArrayBuffer",
        message: "Received a resizable ArrayBuffer."
      });
    }
    return V;
  };
  webidl.converters.TypedArray = function(V, T, prefix, name, opts) {
    if (webidl.util.Type(V) !== "Object" || !types.isTypedArray(V) || V.constructor.name !== T.name) {
      throw webidl.errors.conversionFailed({
        prefix,
        argument: `${name} ("${webidl.util.Stringify(V)}")`,
        types: [T.name]
      });
    }
    if (opts?.allowShared === false && types.isSharedArrayBuffer(V.buffer)) {
      throw webidl.errors.exception({
        header: "ArrayBuffer",
        message: "SharedArrayBuffer is not allowed."
      });
    }
    if (V.buffer.resizable || V.buffer.growable) {
      throw webidl.errors.exception({
        header: "ArrayBuffer",
        message: "Received a resizable ArrayBuffer."
      });
    }
    return V;
  };
  webidl.converters.DataView = function(V, prefix, name, opts) {
    if (webidl.util.Type(V) !== "Object" || !types.isDataView(V)) {
      throw webidl.errors.exception({
        header: prefix,
        message: `${name} is not a DataView.`
      });
    }
    if (opts?.allowShared === false && types.isSharedArrayBuffer(V.buffer)) {
      throw webidl.errors.exception({
        header: "ArrayBuffer",
        message: "SharedArrayBuffer is not allowed."
      });
    }
    if (V.buffer.resizable || V.buffer.growable) {
      throw webidl.errors.exception({
        header: "ArrayBuffer",
        message: "Received a resizable ArrayBuffer."
      });
    }
    return V;
  };
  webidl.converters.BufferSource = function(V, prefix, name, opts) {
    if (types.isAnyArrayBuffer(V)) {
      return webidl.converters.ArrayBuffer(V, prefix, name, { ...opts, allowShared: false });
    }
    if (types.isTypedArray(V)) {
      return webidl.converters.TypedArray(V, V.constructor, prefix, name, { ...opts, allowShared: false });
    }
    if (types.isDataView(V)) {
      return webidl.converters.DataView(V, prefix, name, { ...opts, allowShared: false });
    }
    throw webidl.errors.conversionFailed({
      prefix,
      argument: `${name} ("${webidl.util.Stringify(V)}")`,
      types: ["BufferSource"]
    });
  };
  webidl.converters["sequence<ByteString>"] = webidl.sequenceConverter(webidl.converters.ByteString);
  webidl.converters["sequence<sequence<ByteString>>"] = webidl.sequenceConverter(webidl.converters["sequence<ByteString>"]);
  webidl.converters["record<ByteString, ByteString>"] = webidl.recordConverter(webidl.converters.ByteString, webidl.converters.ByteString);
  module.exports = {
    webidl
  };
});

// node_modules/undici/lib/web/fetch/util.js
var require_util2 = __commonJS((exports, module) => {
  var { Transform } = __require("stream");
  var zlib = __require("zlib");
  var { redirectStatusSet, referrerPolicySet: referrerPolicyTokens, badPortsSet } = require_constants3();
  var { getGlobalOrigin } = require_global();
  var { collectASequenceOfCodePoints, collectAnHTTPQuotedString, removeChars, parseMIMEType } = require_data_url();
  var { performance: performance2 } = __require("perf_hooks");
  var { isBlobLike, ReadableStreamFrom, isValidHTTPToken, normalizedMethodRecordsBase } = require_util();
  var assert = __require("assert");
  var { isUint8Array } = __require("util/types");
  var { webidl } = require_webidl();
  var supportedHashes = [];
  var crypto2;
  try {
    crypto2 = __require("crypto");
    const possibleRelevantHashes = ["sha256", "sha384", "sha512"];
    supportedHashes = crypto2.getHashes().filter((hash) => possibleRelevantHashes.includes(hash));
  } catch {}
  function responseURL(response) {
    const urlList = response.urlList;
    const length = urlList.length;
    return length === 0 ? null : urlList[length - 1].toString();
  }
  function responseLocationURL(response, requestFragment) {
    if (!redirectStatusSet.has(response.status)) {
      return null;
    }
    let location = response.headersList.get("location", true);
    if (location !== null && isValidHeaderValue(location)) {
      if (!isValidEncodedURL(location)) {
        location = normalizeBinaryStringToUtf8(location);
      }
      location = new URL(location, responseURL(response));
    }
    if (location && !location.hash) {
      location.hash = requestFragment;
    }
    return location;
  }
  function isValidEncodedURL(url) {
    for (let i = 0;i < url.length; ++i) {
      const code = url.charCodeAt(i);
      if (code > 126 || code < 32) {
        return false;
      }
    }
    return true;
  }
  function normalizeBinaryStringToUtf8(value) {
    return Buffer.from(value, "binary").toString("utf8");
  }
  function requestCurrentURL(request) {
    return request.urlList[request.urlList.length - 1];
  }
  function requestBadPort(request) {
    const url = requestCurrentURL(request);
    if (urlIsHttpHttpsScheme(url) && badPortsSet.has(url.port)) {
      return "blocked";
    }
    return "allowed";
  }
  function isErrorLike(object) {
    return object instanceof Error || (object?.constructor?.name === "Error" || object?.constructor?.name === "DOMException");
  }
  function isValidReasonPhrase(statusText) {
    for (let i = 0;i < statusText.length; ++i) {
      const c = statusText.charCodeAt(i);
      if (!(c === 9 || c >= 32 && c <= 126 || c >= 128 && c <= 255)) {
        return false;
      }
    }
    return true;
  }
  var isValidHeaderName = isValidHTTPToken;
  function isValidHeaderValue(potentialValue) {
    return (potentialValue[0] === "\t" || potentialValue[0] === " " || potentialValue[potentialValue.length - 1] === "\t" || potentialValue[potentialValue.length - 1] === " " || potentialValue.includes(`
`) || potentialValue.includes("\r") || potentialValue.includes("\x00")) === false;
  }
  function setRequestReferrerPolicyOnRedirect(request, actualResponse) {
    const { headersList } = actualResponse;
    const policyHeader = (headersList.get("referrer-policy", true) ?? "").split(",");
    let policy = "";
    if (policyHeader.length > 0) {
      for (let i = policyHeader.length;i !== 0; i--) {
        const token = policyHeader[i - 1].trim();
        if (referrerPolicyTokens.has(token)) {
          policy = token;
          break;
        }
      }
    }
    if (policy !== "") {
      request.referrerPolicy = policy;
    }
  }
  function crossOriginResourcePolicyCheck() {
    return "allowed";
  }
  function corsCheck() {
    return "success";
  }
  function TAOCheck() {
    return "success";
  }
  function appendFetchMetadata(httpRequest) {
    let header = null;
    header = httpRequest.mode;
    httpRequest.headersList.set("sec-fetch-mode", header, true);
  }
  function appendRequestOriginHeader(request) {
    let serializedOrigin = request.origin;
    if (serializedOrigin === "client" || serializedOrigin === undefined) {
      return;
    }
    if (request.responseTainting === "cors" || request.mode === "websocket") {
      request.headersList.append("origin", serializedOrigin, true);
    } else if (request.method !== "GET" && request.method !== "HEAD") {
      switch (request.referrerPolicy) {
        case "no-referrer":
          serializedOrigin = null;
          break;
        case "no-referrer-when-downgrade":
        case "strict-origin":
        case "strict-origin-when-cross-origin":
          if (request.origin && urlHasHttpsScheme(request.origin) && !urlHasHttpsScheme(requestCurrentURL(request))) {
            serializedOrigin = null;
          }
          break;
        case "same-origin":
          if (!sameOrigin(request, requestCurrentURL(request))) {
            serializedOrigin = null;
          }
          break;
        default:
      }
      request.headersList.append("origin", serializedOrigin, true);
    }
  }
  function coarsenTime(timestamp, crossOriginIsolatedCapability) {
    return timestamp;
  }
  function clampAndCoarsenConnectionTimingInfo(connectionTimingInfo, defaultStartTime, crossOriginIsolatedCapability) {
    if (!connectionTimingInfo?.startTime || connectionTimingInfo.startTime < defaultStartTime) {
      return {
        domainLookupStartTime: defaultStartTime,
        domainLookupEndTime: defaultStartTime,
        connectionStartTime: defaultStartTime,
        connectionEndTime: defaultStartTime,
        secureConnectionStartTime: defaultStartTime,
        ALPNNegotiatedProtocol: connectionTimingInfo?.ALPNNegotiatedProtocol
      };
    }
    return {
      domainLookupStartTime: coarsenTime(connectionTimingInfo.domainLookupStartTime, crossOriginIsolatedCapability),
      domainLookupEndTime: coarsenTime(connectionTimingInfo.domainLookupEndTime, crossOriginIsolatedCapability),
      connectionStartTime: coarsenTime(connectionTimingInfo.connectionStartTime, crossOriginIsolatedCapability),
      connectionEndTime: coarsenTime(connectionTimingInfo.connectionEndTime, crossOriginIsolatedCapability),
      secureConnectionStartTime: coarsenTime(connectionTimingInfo.secureConnectionStartTime, crossOriginIsolatedCapability),
      ALPNNegotiatedProtocol: connectionTimingInfo.ALPNNegotiatedProtocol
    };
  }
  function coarsenedSharedCurrentTime(crossOriginIsolatedCapability) {
    return coarsenTime(performance2.now(), crossOriginIsolatedCapability);
  }
  function createOpaqueTimingInfo(timingInfo) {
    return {
      startTime: timingInfo.startTime ?? 0,
      redirectStartTime: 0,
      redirectEndTime: 0,
      postRedirectStartTime: timingInfo.startTime ?? 0,
      finalServiceWorkerStartTime: 0,
      finalNetworkResponseStartTime: 0,
      finalNetworkRequestStartTime: 0,
      endTime: 0,
      encodedBodySize: 0,
      decodedBodySize: 0,
      finalConnectionTimingInfo: null
    };
  }
  function makePolicyContainer() {
    return {
      referrerPolicy: "strict-origin-when-cross-origin"
    };
  }
  function clonePolicyContainer(policyContainer) {
    return {
      referrerPolicy: policyContainer.referrerPolicy
    };
  }
  function determineRequestsReferrer(request) {
    const policy = request.referrerPolicy;
    assert(policy);
    let referrerSource = null;
    if (request.referrer === "client") {
      const globalOrigin = getGlobalOrigin();
      if (!globalOrigin || globalOrigin.origin === "null") {
        return "no-referrer";
      }
      referrerSource = new URL(globalOrigin);
    } else if (request.referrer instanceof URL) {
      referrerSource = request.referrer;
    }
    let referrerURL = stripURLForReferrer(referrerSource);
    const referrerOrigin = stripURLForReferrer(referrerSource, true);
    if (referrerURL.toString().length > 4096) {
      referrerURL = referrerOrigin;
    }
    const areSameOrigin = sameOrigin(request, referrerURL);
    const isNonPotentiallyTrustWorthy = isURLPotentiallyTrustworthy(referrerURL) && !isURLPotentiallyTrustworthy(request.url);
    switch (policy) {
      case "origin":
        return referrerOrigin != null ? referrerOrigin : stripURLForReferrer(referrerSource, true);
      case "unsafe-url":
        return referrerURL;
      case "same-origin":
        return areSameOrigin ? referrerOrigin : "no-referrer";
      case "origin-when-cross-origin":
        return areSameOrigin ? referrerURL : referrerOrigin;
      case "strict-origin-when-cross-origin": {
        const currentURL = requestCurrentURL(request);
        if (sameOrigin(referrerURL, currentURL)) {
          return referrerURL;
        }
        if (isURLPotentiallyTrustworthy(referrerURL) && !isURLPotentiallyTrustworthy(currentURL)) {
          return "no-referrer";
        }
        return referrerOrigin;
      }
      case "strict-origin":
      case "no-referrer-when-downgrade":
      default:
        return isNonPotentiallyTrustWorthy ? "no-referrer" : referrerOrigin;
    }
  }
  function stripURLForReferrer(url, originOnly) {
    assert(url instanceof URL);
    url = new URL(url);
    if (url.protocol === "file:" || url.protocol === "about:" || url.protocol === "blank:") {
      return "no-referrer";
    }
    url.username = "";
    url.password = "";
    url.hash = "";
    if (originOnly) {
      url.pathname = "";
      url.search = "";
    }
    return url;
  }
  function isURLPotentiallyTrustworthy(url) {
    if (!(url instanceof URL)) {
      return false;
    }
    if (url.href === "about:blank" || url.href === "about:srcdoc") {
      return true;
    }
    if (url.protocol === "data:")
      return true;
    if (url.protocol === "file:")
      return true;
    return isOriginPotentiallyTrustworthy(url.origin);
    function isOriginPotentiallyTrustworthy(origin) {
      if (origin == null || origin === "null")
        return false;
      const originAsURL = new URL(origin);
      if (originAsURL.protocol === "https:" || originAsURL.protocol === "wss:") {
        return true;
      }
      if (/^127(?:\.[0-9]+){0,2}\.[0-9]+$|^\[(?:0*:)*?:?0*1\]$/.test(originAsURL.hostname) || (originAsURL.hostname === "localhost" || originAsURL.hostname.includes("localhost.")) || originAsURL.hostname.endsWith(".localhost")) {
        return true;
      }
      return false;
    }
  }
  function bytesMatch(bytes, metadataList) {
    if (crypto2 === undefined) {
      return true;
    }
    const parsedMetadata = parseMetadata(metadataList);
    if (parsedMetadata === "no metadata") {
      return true;
    }
    if (parsedMetadata.length === 0) {
      return true;
    }
    const strongest = getStrongestMetadata(parsedMetadata);
    const metadata = filterMetadataListByAlgorithm(parsedMetadata, strongest);
    for (const item of metadata) {
      const algorithm = item.algo;
      const expectedValue = item.hash;
      let actualValue = crypto2.createHash(algorithm).update(bytes).digest("base64");
      if (actualValue[actualValue.length - 1] === "=") {
        if (actualValue[actualValue.length - 2] === "=") {
          actualValue = actualValue.slice(0, -2);
        } else {
          actualValue = actualValue.slice(0, -1);
        }
      }
      if (compareBase64Mixed(actualValue, expectedValue)) {
        return true;
      }
    }
    return false;
  }
  var parseHashWithOptions = /(?<algo>sha256|sha384|sha512)-((?<hash>[A-Za-z0-9+/]+|[A-Za-z0-9_-]+)={0,2}(?:\s|$)( +[!-~]*)?)?/i;
  function parseMetadata(metadata) {
    const result = [];
    let empty = true;
    for (const token of metadata.split(" ")) {
      empty = false;
      const parsedToken = parseHashWithOptions.exec(token);
      if (parsedToken === null || parsedToken.groups === undefined || parsedToken.groups.algo === undefined) {
        continue;
      }
      const algorithm = parsedToken.groups.algo.toLowerCase();
      if (supportedHashes.includes(algorithm)) {
        result.push(parsedToken.groups);
      }
    }
    if (empty === true) {
      return "no metadata";
    }
    return result;
  }
  function getStrongestMetadata(metadataList) {
    let algorithm = metadataList[0].algo;
    if (algorithm[3] === "5") {
      return algorithm;
    }
    for (let i = 1;i < metadataList.length; ++i) {
      const metadata = metadataList[i];
      if (metadata.algo[3] === "5") {
        algorithm = "sha512";
        break;
      } else if (algorithm[3] === "3") {
        continue;
      } else if (metadata.algo[3] === "3") {
        algorithm = "sha384";
      }
    }
    return algorithm;
  }
  function filterMetadataListByAlgorithm(metadataList, algorithm) {
    if (metadataList.length === 1) {
      return metadataList;
    }
    let pos = 0;
    for (let i = 0;i < metadataList.length; ++i) {
      if (metadataList[i].algo === algorithm) {
        metadataList[pos++] = metadataList[i];
      }
    }
    metadataList.length = pos;
    return metadataList;
  }
  function compareBase64Mixed(actualValue, expectedValue) {
    if (actualValue.length !== expectedValue.length) {
      return false;
    }
    for (let i = 0;i < actualValue.length; ++i) {
      if (actualValue[i] !== expectedValue[i]) {
        if (actualValue[i] === "+" && expectedValue[i] === "-" || actualValue[i] === "/" && expectedValue[i] === "_") {
          continue;
        }
        return false;
      }
    }
    return true;
  }
  function tryUpgradeRequestToAPotentiallyTrustworthyURL(request) {}
  function sameOrigin(A, B) {
    if (A.origin === B.origin && A.origin === "null") {
      return true;
    }
    if (A.protocol === B.protocol && A.hostname === B.hostname && A.port === B.port) {
      return true;
    }
    return false;
  }
  function createDeferredPromise() {
    let res;
    let rej;
    const promise = new Promise((resolve3, reject) => {
      res = resolve3;
      rej = reject;
    });
    return { promise, resolve: res, reject: rej };
  }
  function isAborted(fetchParams) {
    return fetchParams.controller.state === "aborted";
  }
  function isCancelled(fetchParams) {
    return fetchParams.controller.state === "aborted" || fetchParams.controller.state === "terminated";
  }
  function normalizeMethod(method) {
    return normalizedMethodRecordsBase[method.toLowerCase()] ?? method;
  }
  function serializeJavascriptValueToJSONString(value) {
    const result = JSON.stringify(value);
    if (result === undefined) {
      throw new TypeError("Value is not JSON serializable");
    }
    assert(typeof result === "string");
    return result;
  }
  var esIteratorPrototype = Object.getPrototypeOf(Object.getPrototypeOf([][Symbol.iterator]()));
  function createIterator(name, kInternalIterator, keyIndex = 0, valueIndex = 1) {

    class FastIterableIterator {
      #target;
      #kind;
      #index;
      constructor(target, kind) {
        this.#target = target;
        this.#kind = kind;
        this.#index = 0;
      }
      next() {
        if (typeof this !== "object" || this === null || !(#target in this)) {
          throw new TypeError(`'next' called on an object that does not implement interface ${name} Iterator.`);
        }
        const index = this.#index;
        const values = this.#target[kInternalIterator];
        const len = values.length;
        if (index >= len) {
          return {
            value: undefined,
            done: true
          };
        }
        const { [keyIndex]: key, [valueIndex]: value } = values[index];
        this.#index = index + 1;
        let result;
        switch (this.#kind) {
          case "key":
            result = key;
            break;
          case "value":
            result = value;
            break;
          case "key+value":
            result = [key, value];
            break;
        }
        return {
          value: result,
          done: false
        };
      }
    }
    delete FastIterableIterator.prototype.constructor;
    Object.setPrototypeOf(FastIterableIterator.prototype, esIteratorPrototype);
    Object.defineProperties(FastIterableIterator.prototype, {
      [Symbol.toStringTag]: {
        writable: false,
        enumerable: false,
        configurable: true,
        value: `${name} Iterator`
      },
      next: { writable: true, enumerable: true, configurable: true }
    });
    return function(target, kind) {
      return new FastIterableIterator(target, kind);
    };
  }
  function iteratorMixin(name, object, kInternalIterator, keyIndex = 0, valueIndex = 1) {
    const makeIterator = createIterator(name, kInternalIterator, keyIndex, valueIndex);
    const properties = {
      keys: {
        writable: true,
        enumerable: true,
        configurable: true,
        value: function keys() {
          webidl.brandCheck(this, object);
          return makeIterator(this, "key");
        }
      },
      values: {
        writable: true,
        enumerable: true,
        configurable: true,
        value: function values() {
          webidl.brandCheck(this, object);
          return makeIterator(this, "value");
        }
      },
      entries: {
        writable: true,
        enumerable: true,
        configurable: true,
        value: function entries() {
          webidl.brandCheck(this, object);
          return makeIterator(this, "key+value");
        }
      },
      forEach: {
        writable: true,
        enumerable: true,
        configurable: true,
        value: function forEach(callbackfn, thisArg = globalThis) {
          webidl.brandCheck(this, object);
          webidl.argumentLengthCheck(arguments, 1, `${name}.forEach`);
          if (typeof callbackfn !== "function") {
            throw new TypeError(`Failed to execute 'forEach' on '${name}': parameter 1 is not of type 'Function'.`);
          }
          for (const { 0: key, 1: value } of makeIterator(this, "key+value")) {
            callbackfn.call(thisArg, value, key, this);
          }
        }
      }
    };
    return Object.defineProperties(object.prototype, {
      ...properties,
      [Symbol.iterator]: {
        writable: true,
        enumerable: false,
        configurable: true,
        value: properties.entries.value
      }
    });
  }
  async function fullyReadBody(body, processBody, processBodyError) {
    const successSteps = processBody;
    const errorSteps = processBodyError;
    let reader;
    try {
      reader = body.stream.getReader();
    } catch (e) {
      errorSteps(e);
      return;
    }
    try {
      successSteps(await readAllBytes(reader));
    } catch (e) {
      errorSteps(e);
    }
  }
  function isReadableStreamLike(stream) {
    return stream instanceof ReadableStream || stream[Symbol.toStringTag] === "ReadableStream" && typeof stream.tee === "function";
  }
  function readableStreamClose(controller) {
    try {
      controller.close();
      controller.byobRequest?.respond(0);
    } catch (err) {
      if (!err.message.includes("Controller is already closed") && !err.message.includes("ReadableStream is already closed")) {
        throw err;
      }
    }
  }
  var invalidIsomorphicEncodeValueRegex = /[^\x00-\xFF]/;
  function isomorphicEncode(input) {
    assert(!invalidIsomorphicEncodeValueRegex.test(input));
    return input;
  }
  async function readAllBytes(reader) {
    const bytes = [];
    let byteLength = 0;
    while (true) {
      const { done, value: chunk } = await reader.read();
      if (done) {
        return Buffer.concat(bytes, byteLength);
      }
      if (!isUint8Array(chunk)) {
        throw new TypeError("Received non-Uint8Array chunk");
      }
      bytes.push(chunk);
      byteLength += chunk.length;
    }
  }
  function urlIsLocal(url) {
    assert("protocol" in url);
    const protocol = url.protocol;
    return protocol === "about:" || protocol === "blob:" || protocol === "data:";
  }
  function urlHasHttpsScheme(url) {
    return typeof url === "string" && url[5] === ":" && url[0] === "h" && url[1] === "t" && url[2] === "t" && url[3] === "p" && url[4] === "s" || url.protocol === "https:";
  }
  function urlIsHttpHttpsScheme(url) {
    assert("protocol" in url);
    const protocol = url.protocol;
    return protocol === "http:" || protocol === "https:";
  }
  function simpleRangeHeaderValue(value, allowWhitespace) {
    const data = value;
    if (!data.startsWith("bytes")) {
      return "failure";
    }
    const position = { position: 5 };
    if (allowWhitespace) {
      collectASequenceOfCodePoints((char) => char === "\t" || char === " ", data, position);
    }
    if (data.charCodeAt(position.position) !== 61) {
      return "failure";
    }
    position.position++;
    if (allowWhitespace) {
      collectASequenceOfCodePoints((char) => char === "\t" || char === " ", data, position);
    }
    const rangeStart = collectASequenceOfCodePoints((char) => {
      const code = char.charCodeAt(0);
      return code >= 48 && code <= 57;
    }, data, position);
    const rangeStartValue = rangeStart.length ? Number(rangeStart) : null;
    if (allowWhitespace) {
      collectASequenceOfCodePoints((char) => char === "\t" || char === " ", data, position);
    }
    if (data.charCodeAt(position.position) !== 45) {
      return "failure";
    }
    position.position++;
    if (allowWhitespace) {
      collectASequenceOfCodePoints((char) => char === "\t" || char === " ", data, position);
    }
    const rangeEnd = collectASequenceOfCodePoints((char) => {
      const code = char.charCodeAt(0);
      return code >= 48 && code <= 57;
    }, data, position);
    const rangeEndValue = rangeEnd.length ? Number(rangeEnd) : null;
    if (position.position < data.length) {
      return "failure";
    }
    if (rangeEndValue === null && rangeStartValue === null) {
      return "failure";
    }
    if (rangeStartValue > rangeEndValue) {
      return "failure";
    }
    return { rangeStartValue, rangeEndValue };
  }
  function buildContentRange(rangeStart, rangeEnd, fullLength) {
    let contentRange = "bytes ";
    contentRange += isomorphicEncode(`${rangeStart}`);
    contentRange += "-";
    contentRange += isomorphicEncode(`${rangeEnd}`);
    contentRange += "/";
    contentRange += isomorphicEncode(`${fullLength}`);
    return contentRange;
  }

  class InflateStream extends Transform {
    #zlibOptions;
    constructor(zlibOptions) {
      super();
      this.#zlibOptions = zlibOptions;
    }
    _transform(chunk, encoding, callback) {
      if (!this._inflateStream) {
        if (chunk.length === 0) {
          callback();
          return;
        }
        this._inflateStream = (chunk[0] & 15) === 8 ? zlib.createInflate(this.#zlibOptions) : zlib.createInflateRaw(this.#zlibOptions);
        this._inflateStream.on("data", this.push.bind(this));
        this._inflateStream.on("end", () => this.push(null));
        this._inflateStream.on("error", (err) => this.destroy(err));
      }
      this._inflateStream.write(chunk, encoding, callback);
    }
    _final(callback) {
      if (this._inflateStream) {
        this._inflateStream.end();
        this._inflateStream = null;
      }
      callback();
    }
  }
  function createInflate(zlibOptions) {
    return new InflateStream(zlibOptions);
  }
  function extractMimeType(headers) {
    let charset = null;
    let essence = null;
    let mimeType = null;
    const values = getDecodeSplit("content-type", headers);
    if (values === null) {
      return "failure";
    }
    for (const value of values) {
      const temporaryMimeType = parseMIMEType(value);
      if (temporaryMimeType === "failure" || temporaryMimeType.essence === "*/*") {
        continue;
      }
      mimeType = temporaryMimeType;
      if (mimeType.essence !== essence) {
        charset = null;
        if (mimeType.parameters.has("charset")) {
          charset = mimeType.parameters.get("charset");
        }
        essence = mimeType.essence;
      } else if (!mimeType.parameters.has("charset") && charset !== null) {
        mimeType.parameters.set("charset", charset);
      }
    }
    if (mimeType == null) {
      return "failure";
    }
    return mimeType;
  }
  function gettingDecodingSplitting(value) {
    const input = value;
    const position = { position: 0 };
    const values = [];
    let temporaryValue = "";
    while (position.position < input.length) {
      temporaryValue += collectASequenceOfCodePoints((char) => char !== '"' && char !== ",", input, position);
      if (position.position < input.length) {
        if (input.charCodeAt(position.position) === 34) {
          temporaryValue += collectAnHTTPQuotedString(input, position);
          if (position.position < input.length) {
            continue;
          }
        } else {
          assert(input.charCodeAt(position.position) === 44);
          position.position++;
        }
      }
      temporaryValue = removeChars(temporaryValue, true, true, (char) => char === 9 || char === 32);
      values.push(temporaryValue);
      temporaryValue = "";
    }
    return values;
  }
  function getDecodeSplit(name, list) {
    const value = list.get(name, true);
    if (value === null) {
      return null;
    }
    return gettingDecodingSplitting(value);
  }
  var textDecoder = new TextDecoder;
  function utf8DecodeBytes(buffer) {
    if (buffer.length === 0) {
      return "";
    }
    if (buffer[0] === 239 && buffer[1] === 187 && buffer[2] === 191) {
      buffer = buffer.subarray(3);
    }
    const output = textDecoder.decode(buffer);
    return output;
  }

  class EnvironmentSettingsObjectBase {
    get baseUrl() {
      return getGlobalOrigin();
    }
    get origin() {
      return this.baseUrl?.origin;
    }
    policyContainer = makePolicyContainer();
  }

  class EnvironmentSettingsObject {
    settingsObject = new EnvironmentSettingsObjectBase;
  }
  var environmentSettingsObject = new EnvironmentSettingsObject;
  module.exports = {
    isAborted,
    isCancelled,
    isValidEncodedURL,
    createDeferredPromise,
    ReadableStreamFrom,
    tryUpgradeRequestToAPotentiallyTrustworthyURL,
    clampAndCoarsenConnectionTimingInfo,
    coarsenedSharedCurrentTime,
    determineRequestsReferrer,
    makePolicyContainer,
    clonePolicyContainer,
    appendFetchMetadata,
    appendRequestOriginHeader,
    TAOCheck,
    corsCheck,
    crossOriginResourcePolicyCheck,
    createOpaqueTimingInfo,
    setRequestReferrerPolicyOnRedirect,
    isValidHTTPToken,
    requestBadPort,
    requestCurrentURL,
    responseURL,
    responseLocationURL,
    isBlobLike,
    isURLPotentiallyTrustworthy,
    isValidReasonPhrase,
    sameOrigin,
    normalizeMethod,
    serializeJavascriptValueToJSONString,
    iteratorMixin,
    createIterator,
    isValidHeaderName,
    isValidHeaderValue,
    isErrorLike,
    fullyReadBody,
    bytesMatch,
    isReadableStreamLike,
    readableStreamClose,
    isomorphicEncode,
    urlIsLocal,
    urlHasHttpsScheme,
    urlIsHttpHttpsScheme,
    readAllBytes,
    simpleRangeHeaderValue,
    buildContentRange,
    parseMetadata,
    createInflate,
    extractMimeType,
    getDecodeSplit,
    utf8DecodeBytes,
    environmentSettingsObject
  };
});

// node_modules/undici/lib/web/fetch/symbols.js
var require_symbols2 = __commonJS((exports, module) => {
  module.exports = {
    kUrl: Symbol("url"),
    kHeaders: Symbol("headers"),
    kSignal: Symbol("signal"),
    kState: Symbol("state"),
    kDispatcher: Symbol("dispatcher")
  };
});

// node_modules/undici/lib/web/fetch/file.js
var require_file = __commonJS((exports, module) => {
  var { Blob: Blob2, File } = __require("buffer");
  var { kState } = require_symbols2();
  var { webidl } = require_webidl();

  class FileLike {
    constructor(blobLike, fileName, options = {}) {
      const n = fileName;
      const t = options.type;
      const d = options.lastModified ?? Date.now();
      this[kState] = {
        blobLike,
        name: n,
        type: t,
        lastModified: d
      };
    }
    stream(...args) {
      webidl.brandCheck(this, FileLike);
      return this[kState].blobLike.stream(...args);
    }
    arrayBuffer(...args) {
      webidl.brandCheck(this, FileLike);
      return this[kState].blobLike.arrayBuffer(...args);
    }
    slice(...args) {
      webidl.brandCheck(this, FileLike);
      return this[kState].blobLike.slice(...args);
    }
    text(...args) {
      webidl.brandCheck(this, FileLike);
      return this[kState].blobLike.text(...args);
    }
    get size() {
      webidl.brandCheck(this, FileLike);
      return this[kState].blobLike.size;
    }
    get type() {
      webidl.brandCheck(this, FileLike);
      return this[kState].blobLike.type;
    }
    get name() {
      webidl.brandCheck(this, FileLike);
      return this[kState].name;
    }
    get lastModified() {
      webidl.brandCheck(this, FileLike);
      return this[kState].lastModified;
    }
    get [Symbol.toStringTag]() {
      return "File";
    }
  }
  webidl.converters.Blob = webidl.interfaceConverter(Blob2);
  function isFileLike(object) {
    return object instanceof File || object && (typeof object.stream === "function" || typeof object.arrayBuffer === "function") && object[Symbol.toStringTag] === "File";
  }
  module.exports = { FileLike, isFileLike };
});

// node_modules/undici/lib/web/fetch/formdata.js
var require_formdata = __commonJS((exports, module) => {
  var { isBlobLike, iteratorMixin } = require_util2();
  var { kState } = require_symbols2();
  var { kEnumerableProperty } = require_util();
  var { FileLike, isFileLike } = require_file();
  var { webidl } = require_webidl();
  var { File: NativeFile } = __require("buffer");
  var nodeUtil = __require("util");
  var File = globalThis.File ?? NativeFile;

  class FormData {
    constructor(form) {
      webidl.util.markAsUncloneable(this);
      if (form !== undefined) {
        throw webidl.errors.conversionFailed({
          prefix: "FormData constructor",
          argument: "Argument 1",
          types: ["undefined"]
        });
      }
      this[kState] = [];
    }
    append(name, value, filename = undefined) {
      webidl.brandCheck(this, FormData);
      const prefix = "FormData.append";
      webidl.argumentLengthCheck(arguments, 2, prefix);
      if (arguments.length === 3 && !isBlobLike(value)) {
        throw new TypeError("Failed to execute 'append' on 'FormData': parameter 2 is not of type 'Blob'");
      }
      name = webidl.converters.USVString(name, prefix, "name");
      value = isBlobLike(value) ? webidl.converters.Blob(value, prefix, "value", { strict: false }) : webidl.converters.USVString(value, prefix, "value");
      filename = arguments.length === 3 ? webidl.converters.USVString(filename, prefix, "filename") : undefined;
      const entry = makeEntry(name, value, filename);
      this[kState].push(entry);
    }
    delete(name) {
      webidl.brandCheck(this, FormData);
      const prefix = "FormData.delete";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      name = webidl.converters.USVString(name, prefix, "name");
      this[kState] = this[kState].filter((entry) => entry.name !== name);
    }
    get(name) {
      webidl.brandCheck(this, FormData);
      const prefix = "FormData.get";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      name = webidl.converters.USVString(name, prefix, "name");
      const idx = this[kState].findIndex((entry) => entry.name === name);
      if (idx === -1) {
        return null;
      }
      return this[kState][idx].value;
    }
    getAll(name) {
      webidl.brandCheck(this, FormData);
      const prefix = "FormData.getAll";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      name = webidl.converters.USVString(name, prefix, "name");
      return this[kState].filter((entry) => entry.name === name).map((entry) => entry.value);
    }
    has(name) {
      webidl.brandCheck(this, FormData);
      const prefix = "FormData.has";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      name = webidl.converters.USVString(name, prefix, "name");
      return this[kState].findIndex((entry) => entry.name === name) !== -1;
    }
    set(name, value, filename = undefined) {
      webidl.brandCheck(this, FormData);
      const prefix = "FormData.set";
      webidl.argumentLengthCheck(arguments, 2, prefix);
      if (arguments.length === 3 && !isBlobLike(value)) {
        throw new TypeError("Failed to execute 'set' on 'FormData': parameter 2 is not of type 'Blob'");
      }
      name = webidl.converters.USVString(name, prefix, "name");
      value = isBlobLike(value) ? webidl.converters.Blob(value, prefix, "name", { strict: false }) : webidl.converters.USVString(value, prefix, "name");
      filename = arguments.length === 3 ? webidl.converters.USVString(filename, prefix, "name") : undefined;
      const entry = makeEntry(name, value, filename);
      const idx = this[kState].findIndex((entry2) => entry2.name === name);
      if (idx !== -1) {
        this[kState] = [
          ...this[kState].slice(0, idx),
          entry,
          ...this[kState].slice(idx + 1).filter((entry2) => entry2.name !== name)
        ];
      } else {
        this[kState].push(entry);
      }
    }
    [nodeUtil.inspect.custom](depth, options) {
      const state = this[kState].reduce((a, b) => {
        if (a[b.name]) {
          if (Array.isArray(a[b.name])) {
            a[b.name].push(b.value);
          } else {
            a[b.name] = [a[b.name], b.value];
          }
        } else {
          a[b.name] = b.value;
        }
        return a;
      }, { __proto__: null });
      options.depth ??= depth;
      options.colors ??= true;
      const output = nodeUtil.formatWithOptions(options, state);
      return `FormData ${output.slice(output.indexOf("]") + 2)}`;
    }
  }
  iteratorMixin("FormData", FormData, kState, "name", "value");
  Object.defineProperties(FormData.prototype, {
    append: kEnumerableProperty,
    delete: kEnumerableProperty,
    get: kEnumerableProperty,
    getAll: kEnumerableProperty,
    has: kEnumerableProperty,
    set: kEnumerableProperty,
    [Symbol.toStringTag]: {
      value: "FormData",
      configurable: true
    }
  });
  function makeEntry(name, value, filename) {
    if (typeof value === "string") {} else {
      if (!isFileLike(value)) {
        value = value instanceof Blob ? new File([value], "blob", { type: value.type }) : new FileLike(value, "blob", { type: value.type });
      }
      if (filename !== undefined) {
        const options = {
          type: value.type,
          lastModified: value.lastModified
        };
        value = value instanceof NativeFile ? new File([value], filename, options) : new FileLike(value, filename, options);
      }
    }
    return { name, value };
  }
  module.exports = { FormData, makeEntry };
});

// node_modules/undici/lib/web/fetch/formdata-parser.js
var require_formdata_parser = __commonJS((exports, module) => {
  var { isUSVString, bufferToLowerCasedHeaderName } = require_util();
  var { utf8DecodeBytes } = require_util2();
  var { HTTP_TOKEN_CODEPOINTS, isomorphicDecode } = require_data_url();
  var { isFileLike } = require_file();
  var { makeEntry } = require_formdata();
  var assert = __require("assert");
  var { File: NodeFile } = __require("buffer");
  var File = globalThis.File ?? NodeFile;
  var formDataNameBuffer = Buffer.from('form-data; name="');
  var filenameBuffer = Buffer.from("; filename");
  var dd = Buffer.from("--");
  var ddcrlf = Buffer.from(`--\r
`);
  function isAsciiString(chars) {
    for (let i = 0;i < chars.length; ++i) {
      if ((chars.charCodeAt(i) & ~127) !== 0) {
        return false;
      }
    }
    return true;
  }
  function validateBoundary(boundary) {
    const length = boundary.length;
    if (length < 27 || length > 70) {
      return false;
    }
    for (let i = 0;i < length; ++i) {
      const cp = boundary.charCodeAt(i);
      if (!(cp >= 48 && cp <= 57 || cp >= 65 && cp <= 90 || cp >= 97 && cp <= 122 || cp === 39 || cp === 45 || cp === 95)) {
        return false;
      }
    }
    return true;
  }
  function multipartFormDataParser(input, mimeType) {
    assert(mimeType !== "failure" && mimeType.essence === "multipart/form-data");
    const boundaryString = mimeType.parameters.get("boundary");
    if (boundaryString === undefined) {
      return "failure";
    }
    const boundary = Buffer.from(`--${boundaryString}`, "utf8");
    const entryList = [];
    const position = { position: 0 };
    while (input[position.position] === 13 && input[position.position + 1] === 10) {
      position.position += 2;
    }
    let trailing = input.length;
    while (input[trailing - 1] === 10 && input[trailing - 2] === 13) {
      trailing -= 2;
    }
    if (trailing !== input.length) {
      input = input.subarray(0, trailing);
    }
    while (true) {
      if (input.subarray(position.position, position.position + boundary.length).equals(boundary)) {
        position.position += boundary.length;
      } else {
        return "failure";
      }
      if (position.position === input.length - 2 && bufferStartsWith(input, dd, position) || position.position === input.length - 4 && bufferStartsWith(input, ddcrlf, position)) {
        return entryList;
      }
      if (input[position.position] !== 13 || input[position.position + 1] !== 10) {
        return "failure";
      }
      position.position += 2;
      const result = parseMultipartFormDataHeaders(input, position);
      if (result === "failure") {
        return "failure";
      }
      let { name, filename, contentType, encoding } = result;
      position.position += 2;
      let body;
      {
        const boundaryIndex = input.indexOf(boundary.subarray(2), position.position);
        if (boundaryIndex === -1) {
          return "failure";
        }
        body = input.subarray(position.position, boundaryIndex - 4);
        position.position += body.length;
        if (encoding === "base64") {
          body = Buffer.from(body.toString(), "base64");
        }
      }
      if (input[position.position] !== 13 || input[position.position + 1] !== 10) {
        return "failure";
      } else {
        position.position += 2;
      }
      let value;
      if (filename !== null) {
        contentType ??= "text/plain";
        if (!isAsciiString(contentType)) {
          contentType = "";
        }
        value = new File([body], filename, { type: contentType });
      } else {
        value = utf8DecodeBytes(Buffer.from(body));
      }
      assert(isUSVString(name));
      assert(typeof value === "string" && isUSVString(value) || isFileLike(value));
      entryList.push(makeEntry(name, value, filename));
    }
  }
  function parseMultipartFormDataHeaders(input, position) {
    let name = null;
    let filename = null;
    let contentType = null;
    let encoding = null;
    while (true) {
      if (input[position.position] === 13 && input[position.position + 1] === 10) {
        if (name === null) {
          return "failure";
        }
        return { name, filename, contentType, encoding };
      }
      let headerName = collectASequenceOfBytes((char) => char !== 10 && char !== 13 && char !== 58, input, position);
      headerName = removeChars(headerName, true, true, (char) => char === 9 || char === 32);
      if (!HTTP_TOKEN_CODEPOINTS.test(headerName.toString())) {
        return "failure";
      }
      if (input[position.position] !== 58) {
        return "failure";
      }
      position.position++;
      collectASequenceOfBytes((char) => char === 32 || char === 9, input, position);
      switch (bufferToLowerCasedHeaderName(headerName)) {
        case "content-disposition": {
          name = filename = null;
          if (!bufferStartsWith(input, formDataNameBuffer, position)) {
            return "failure";
          }
          position.position += 17;
          name = parseMultipartFormDataName(input, position);
          if (name === null) {
            return "failure";
          }
          if (bufferStartsWith(input, filenameBuffer, position)) {
            let check = position.position + filenameBuffer.length;
            if (input[check] === 42) {
              position.position += 1;
              check += 1;
            }
            if (input[check] !== 61 || input[check + 1] !== 34) {
              return "failure";
            }
            position.position += 12;
            filename = parseMultipartFormDataName(input, position);
            if (filename === null) {
              return "failure";
            }
          }
          break;
        }
        case "content-type": {
          let headerValue = collectASequenceOfBytes((char) => char !== 10 && char !== 13, input, position);
          headerValue = removeChars(headerValue, false, true, (char) => char === 9 || char === 32);
          contentType = isomorphicDecode(headerValue);
          break;
        }
        case "content-transfer-encoding": {
          let headerValue = collectASequenceOfBytes((char) => char !== 10 && char !== 13, input, position);
          headerValue = removeChars(headerValue, false, true, (char) => char === 9 || char === 32);
          encoding = isomorphicDecode(headerValue);
          break;
        }
        default: {
          collectASequenceOfBytes((char) => char !== 10 && char !== 13, input, position);
        }
      }
      if (input[position.position] !== 13 && input[position.position + 1] !== 10) {
        return "failure";
      } else {
        position.position += 2;
      }
    }
  }
  function parseMultipartFormDataName(input, position) {
    assert(input[position.position - 1] === 34);
    let name = collectASequenceOfBytes((char) => char !== 10 && char !== 13 && char !== 34, input, position);
    if (input[position.position] !== 34) {
      return null;
    } else {
      position.position++;
    }
    name = new TextDecoder().decode(name).replace(/%0A/ig, `
`).replace(/%0D/ig, "\r").replace(/%22/g, '"');
    return name;
  }
  function collectASequenceOfBytes(condition, input, position) {
    let start = position.position;
    while (start < input.length && condition(input[start])) {
      ++start;
    }
    return input.subarray(position.position, position.position = start);
  }
  function removeChars(buf, leading, trailing, predicate) {
    let lead = 0;
    let trail = buf.length - 1;
    if (leading) {
      while (lead < buf.length && predicate(buf[lead]))
        lead++;
    }
    if (trailing) {
      while (trail > 0 && predicate(buf[trail]))
        trail--;
    }
    return lead === 0 && trail === buf.length - 1 ? buf : buf.subarray(lead, trail + 1);
  }
  function bufferStartsWith(buffer, start, position) {
    if (buffer.length < start.length) {
      return false;
    }
    for (let i = 0;i < start.length; i++) {
      if (start[i] !== buffer[position.position + i]) {
        return false;
      }
    }
    return true;
  }
  module.exports = {
    multipartFormDataParser,
    validateBoundary
  };
});

// node_modules/undici/lib/web/fetch/body.js
var require_body = __commonJS((exports, module) => {
  var util = require_util();
  var {
    ReadableStreamFrom,
    isBlobLike,
    isReadableStreamLike,
    readableStreamClose,
    createDeferredPromise,
    fullyReadBody,
    extractMimeType,
    utf8DecodeBytes
  } = require_util2();
  var { FormData } = require_formdata();
  var { kState } = require_symbols2();
  var { webidl } = require_webidl();
  var { Blob: Blob2 } = __require("buffer");
  var assert = __require("assert");
  var { isErrored, isDisturbed } = __require("stream");
  var { isArrayBuffer } = __require("util/types");
  var { serializeAMimeType } = require_data_url();
  var { multipartFormDataParser } = require_formdata_parser();
  var random;
  try {
    const crypto2 = __require("crypto");
    random = (max) => crypto2.randomInt(0, max);
  } catch {
    random = (max) => Math.floor(Math.random(max));
  }
  var textEncoder = new TextEncoder;
  function noop() {}
  var hasFinalizationRegistry = globalThis.FinalizationRegistry && process.version.indexOf("v18") !== 0;
  var streamRegistry;
  if (hasFinalizationRegistry) {
    streamRegistry = new FinalizationRegistry((weakRef) => {
      const stream = weakRef.deref();
      if (stream && !stream.locked && !isDisturbed(stream) && !isErrored(stream)) {
        stream.cancel("Response object has been garbage collected").catch(noop);
      }
    });
  }
  function extractBody(object, keepalive = false) {
    let stream = null;
    if (object instanceof ReadableStream) {
      stream = object;
    } else if (isBlobLike(object)) {
      stream = object.stream();
    } else {
      stream = new ReadableStream({
        async pull(controller) {
          const buffer = typeof source === "string" ? textEncoder.encode(source) : source;
          if (buffer.byteLength) {
            controller.enqueue(buffer);
          }
          queueMicrotask(() => readableStreamClose(controller));
        },
        start() {},
        type: "bytes"
      });
    }
    assert(isReadableStreamLike(stream));
    let action = null;
    let source = null;
    let length = null;
    let type = null;
    if (typeof object === "string") {
      source = object;
      type = "text/plain;charset=UTF-8";
    } else if (object instanceof URLSearchParams) {
      source = object.toString();
      type = "application/x-www-form-urlencoded;charset=UTF-8";
    } else if (isArrayBuffer(object)) {
      source = new Uint8Array(object.slice());
    } else if (ArrayBuffer.isView(object)) {
      source = new Uint8Array(object.buffer.slice(object.byteOffset, object.byteOffset + object.byteLength));
    } else if (util.isFormDataLike(object)) {
      const boundary = `----formdata-undici-0${`${random(100000000000)}`.padStart(11, "0")}`;
      const prefix = `--${boundary}\r
Content-Disposition: form-data`;
      /*! formdata-polyfill. MIT License. Jimmy Wärting <https://jimmy.warting.se/opensource> */
      const escape = (str) => str.replace(/\n/g, "%0A").replace(/\r/g, "%0D").replace(/"/g, "%22");
      const normalizeLinefeeds = (value) => value.replace(/\r?\n|\r/g, `\r
`);
      const blobParts = [];
      const rn = new Uint8Array([13, 10]);
      length = 0;
      let hasUnknownSizeValue = false;
      for (const [name, value] of object) {
        if (typeof value === "string") {
          const chunk2 = textEncoder.encode(prefix + `; name="${escape(normalizeLinefeeds(name))}"` + `\r
\r
${normalizeLinefeeds(value)}\r
`);
          blobParts.push(chunk2);
          length += chunk2.byteLength;
        } else {
          const chunk2 = textEncoder.encode(`${prefix}; name="${escape(normalizeLinefeeds(name))}"` + (value.name ? `; filename="${escape(value.name)}"` : "") + `\r
` + `Content-Type: ${value.type || "application/octet-stream"}\r
\r
`);
          blobParts.push(chunk2, value, rn);
          if (typeof value.size === "number") {
            length += chunk2.byteLength + value.size + rn.byteLength;
          } else {
            hasUnknownSizeValue = true;
          }
        }
      }
      const chunk = textEncoder.encode(`--${boundary}--\r
`);
      blobParts.push(chunk);
      length += chunk.byteLength;
      if (hasUnknownSizeValue) {
        length = null;
      }
      source = object;
      action = async function* () {
        for (const part of blobParts) {
          if (part.stream) {
            yield* part.stream();
          } else {
            yield part;
          }
        }
      };
      type = `multipart/form-data; boundary=${boundary}`;
    } else if (isBlobLike(object)) {
      source = object;
      length = object.size;
      if (object.type) {
        type = object.type;
      }
    } else if (typeof object[Symbol.asyncIterator] === "function") {
      if (keepalive) {
        throw new TypeError("keepalive");
      }
      if (util.isDisturbed(object) || object.locked) {
        throw new TypeError("Response body object should not be disturbed or locked");
      }
      stream = object instanceof ReadableStream ? object : ReadableStreamFrom(object);
    }
    if (typeof source === "string" || util.isBuffer(source)) {
      length = Buffer.byteLength(source);
    }
    if (action != null) {
      let iterator;
      stream = new ReadableStream({
        async start() {
          iterator = action(object)[Symbol.asyncIterator]();
        },
        async pull(controller) {
          const { value, done } = await iterator.next();
          if (done) {
            queueMicrotask(() => {
              controller.close();
              controller.byobRequest?.respond(0);
            });
          } else {
            if (!isErrored(stream)) {
              const buffer = new Uint8Array(value);
              if (buffer.byteLength) {
                controller.enqueue(buffer);
              }
            }
          }
          return controller.desiredSize > 0;
        },
        async cancel(reason) {
          await iterator.return();
        },
        type: "bytes"
      });
    }
    const body = { stream, source, length };
    return [body, type];
  }
  function safelyExtractBody(object, keepalive = false) {
    if (object instanceof ReadableStream) {
      assert(!util.isDisturbed(object), "The body has already been consumed.");
      assert(!object.locked, "The stream is locked.");
    }
    return extractBody(object, keepalive);
  }
  function cloneBody(instance, body) {
    const [out1, out2] = body.stream.tee();
    body.stream = out1;
    return {
      stream: out2,
      length: body.length,
      source: body.source
    };
  }
  function throwIfAborted(state) {
    if (state.aborted) {
      throw new DOMException("The operation was aborted.", "AbortError");
    }
  }
  function bodyMixinMethods(instance) {
    const methods = {
      blob() {
        return consumeBody(this, (bytes) => {
          let mimeType = bodyMimeType(this);
          if (mimeType === null) {
            mimeType = "";
          } else if (mimeType) {
            mimeType = serializeAMimeType(mimeType);
          }
          return new Blob2([bytes], { type: mimeType });
        }, instance);
      },
      arrayBuffer() {
        return consumeBody(this, (bytes) => {
          return new Uint8Array(bytes).buffer;
        }, instance);
      },
      text() {
        return consumeBody(this, utf8DecodeBytes, instance);
      },
      json() {
        return consumeBody(this, parseJSONFromBytes, instance);
      },
      formData() {
        return consumeBody(this, (value) => {
          const mimeType = bodyMimeType(this);
          if (mimeType !== null) {
            switch (mimeType.essence) {
              case "multipart/form-data": {
                const parsed = multipartFormDataParser(value, mimeType);
                if (parsed === "failure") {
                  throw new TypeError("Failed to parse body as FormData.");
                }
                const fd = new FormData;
                fd[kState] = parsed;
                return fd;
              }
              case "application/x-www-form-urlencoded": {
                const entries = new URLSearchParams(value.toString());
                const fd = new FormData;
                for (const [name, value2] of entries) {
                  fd.append(name, value2);
                }
                return fd;
              }
            }
          }
          throw new TypeError('Content-Type was not one of "multipart/form-data" or "application/x-www-form-urlencoded".');
        }, instance);
      },
      bytes() {
        return consumeBody(this, (bytes) => {
          return new Uint8Array(bytes);
        }, instance);
      }
    };
    return methods;
  }
  function mixinBody(prototype) {
    Object.assign(prototype.prototype, bodyMixinMethods(prototype));
  }
  async function consumeBody(object, convertBytesToJSValue, instance) {
    webidl.brandCheck(object, instance);
    if (bodyUnusable(object)) {
      throw new TypeError("Body is unusable: Body has already been read");
    }
    throwIfAborted(object[kState]);
    const promise = createDeferredPromise();
    const errorSteps = (error) => promise.reject(error);
    const successSteps = (data) => {
      try {
        promise.resolve(convertBytesToJSValue(data));
      } catch (e) {
        errorSteps(e);
      }
    };
    if (object[kState].body == null) {
      successSteps(Buffer.allocUnsafe(0));
      return promise.promise;
    }
    await fullyReadBody(object[kState].body, successSteps, errorSteps);
    return promise.promise;
  }
  function bodyUnusable(object) {
    const body = object[kState].body;
    return body != null && (body.stream.locked || util.isDisturbed(body.stream));
  }
  function parseJSONFromBytes(bytes) {
    return JSON.parse(utf8DecodeBytes(bytes));
  }
  function bodyMimeType(requestOrResponse) {
    const headers = requestOrResponse[kState].headersList;
    const mimeType = extractMimeType(headers);
    if (mimeType === "failure") {
      return null;
    }
    return mimeType;
  }
  module.exports = {
    extractBody,
    safelyExtractBody,
    cloneBody,
    mixinBody,
    streamRegistry,
    hasFinalizationRegistry,
    bodyUnusable
  };
});

// node_modules/undici/lib/dispatcher/client-h1.js
var require_client_h1 = __commonJS((exports, module) => {
  var assert = __require("assert");
  var util = require_util();
  var { channels } = require_diagnostics();
  var timers = require_timers();
  var {
    RequestContentLengthMismatchError,
    ResponseContentLengthMismatchError,
    RequestAbortedError,
    HeadersTimeoutError,
    HeadersOverflowError,
    SocketError,
    InformationalError,
    BodyTimeoutError,
    HTTPParserError,
    ResponseExceededMaxSizeError
  } = require_errors();
  var {
    kUrl,
    kReset,
    kClient,
    kParser,
    kBlocking,
    kRunning,
    kPending,
    kSize,
    kWriting,
    kQueue,
    kNoRef,
    kKeepAliveDefaultTimeout,
    kHostHeader,
    kPendingIdx,
    kRunningIdx,
    kError,
    kPipelining,
    kSocket,
    kKeepAliveTimeoutValue,
    kMaxHeadersSize,
    kKeepAliveMaxTimeout,
    kKeepAliveTimeoutThreshold,
    kHeadersTimeout,
    kBodyTimeout,
    kStrictContentLength,
    kMaxRequests,
    kCounter,
    kMaxResponseSize,
    kOnError,
    kResume,
    kHTTPContext
  } = require_symbols();
  var constants = require_constants2();
  var EMPTY_BUF = Buffer.alloc(0);
  var FastBuffer = Buffer[Symbol.species];
  var addListener = util.addListener;
  var removeAllListeners = util.removeAllListeners;
  var extractBody;
  async function lazyllhttp() {
    const llhttpWasmData = process.env.JEST_WORKER_ID ? require_llhttp_wasm() : undefined;
    let mod;
    try {
      mod = await WebAssembly.compile(require_llhttp_simd_wasm());
    } catch (e) {
      mod = await WebAssembly.compile(llhttpWasmData || require_llhttp_wasm());
    }
    return await WebAssembly.instantiate(mod, {
      env: {
        wasm_on_url: (p, at, len) => {
          return 0;
        },
        wasm_on_status: (p, at, len) => {
          assert(currentParser.ptr === p);
          const start = at - currentBufferPtr + currentBufferRef.byteOffset;
          return currentParser.onStatus(new FastBuffer(currentBufferRef.buffer, start, len)) || 0;
        },
        wasm_on_message_begin: (p) => {
          assert(currentParser.ptr === p);
          return currentParser.onMessageBegin() || 0;
        },
        wasm_on_header_field: (p, at, len) => {
          assert(currentParser.ptr === p);
          const start = at - currentBufferPtr + currentBufferRef.byteOffset;
          return currentParser.onHeaderField(new FastBuffer(currentBufferRef.buffer, start, len)) || 0;
        },
        wasm_on_header_value: (p, at, len) => {
          assert(currentParser.ptr === p);
          const start = at - currentBufferPtr + currentBufferRef.byteOffset;
          return currentParser.onHeaderValue(new FastBuffer(currentBufferRef.buffer, start, len)) || 0;
        },
        wasm_on_headers_complete: (p, statusCode, upgrade, shouldKeepAlive) => {
          assert(currentParser.ptr === p);
          return currentParser.onHeadersComplete(statusCode, Boolean(upgrade), Boolean(shouldKeepAlive)) || 0;
        },
        wasm_on_body: (p, at, len) => {
          assert(currentParser.ptr === p);
          const start = at - currentBufferPtr + currentBufferRef.byteOffset;
          return currentParser.onBody(new FastBuffer(currentBufferRef.buffer, start, len)) || 0;
        },
        wasm_on_message_complete: (p) => {
          assert(currentParser.ptr === p);
          return currentParser.onMessageComplete() || 0;
        }
      }
    });
  }
  var llhttpInstance = null;
  var llhttpPromise = lazyllhttp();
  llhttpPromise.catch();
  var currentParser = null;
  var currentBufferRef = null;
  var currentBufferSize = 0;
  var currentBufferPtr = null;
  var USE_NATIVE_TIMER = 0;
  var USE_FAST_TIMER = 1;
  var TIMEOUT_HEADERS = 2 | USE_FAST_TIMER;
  var TIMEOUT_BODY = 4 | USE_FAST_TIMER;
  var TIMEOUT_KEEP_ALIVE = 8 | USE_NATIVE_TIMER;

  class Parser {
    constructor(client, socket, { exports: exports2 }) {
      assert(Number.isFinite(client[kMaxHeadersSize]) && client[kMaxHeadersSize] > 0);
      this.llhttp = exports2;
      this.ptr = this.llhttp.llhttp_alloc(constants.TYPE.RESPONSE);
      this.client = client;
      this.socket = socket;
      this.timeout = null;
      this.timeoutValue = null;
      this.timeoutType = null;
      this.statusCode = null;
      this.statusText = "";
      this.upgrade = false;
      this.headers = [];
      this.headersSize = 0;
      this.headersMaxSize = client[kMaxHeadersSize];
      this.shouldKeepAlive = false;
      this.paused = false;
      this.resume = this.resume.bind(this);
      this.bytesRead = 0;
      this.keepAlive = "";
      this.contentLength = "";
      this.connection = "";
      this.maxResponseSize = client[kMaxResponseSize];
    }
    setTimeout(delay, type) {
      if (delay !== this.timeoutValue || type & USE_FAST_TIMER ^ this.timeoutType & USE_FAST_TIMER) {
        if (this.timeout) {
          timers.clearTimeout(this.timeout);
          this.timeout = null;
        }
        if (delay) {
          if (type & USE_FAST_TIMER) {
            this.timeout = timers.setFastTimeout(onParserTimeout, delay, new WeakRef(this));
          } else {
            this.timeout = setTimeout(onParserTimeout, delay, new WeakRef(this));
            this.timeout.unref();
          }
        }
        this.timeoutValue = delay;
      } else if (this.timeout) {
        if (this.timeout.refresh) {
          this.timeout.refresh();
        }
      }
      this.timeoutType = type;
    }
    resume() {
      if (this.socket.destroyed || !this.paused) {
        return;
      }
      assert(this.ptr != null);
      assert(currentParser == null);
      this.llhttp.llhttp_resume(this.ptr);
      assert(this.timeoutType === TIMEOUT_BODY);
      if (this.timeout) {
        if (this.timeout.refresh) {
          this.timeout.refresh();
        }
      }
      this.paused = false;
      this.execute(this.socket.read() || EMPTY_BUF);
      this.readMore();
    }
    readMore() {
      while (!this.paused && this.ptr) {
        const chunk = this.socket.read();
        if (chunk === null) {
          break;
        }
        this.execute(chunk);
      }
    }
    execute(data) {
      assert(this.ptr != null);
      assert(currentParser == null);
      assert(!this.paused);
      const { socket, llhttp } = this;
      if (data.length > currentBufferSize) {
        if (currentBufferPtr) {
          llhttp.free(currentBufferPtr);
        }
        currentBufferSize = Math.ceil(data.length / 4096) * 4096;
        currentBufferPtr = llhttp.malloc(currentBufferSize);
      }
      new Uint8Array(llhttp.memory.buffer, currentBufferPtr, currentBufferSize).set(data);
      try {
        let ret;
        try {
          currentBufferRef = data;
          currentParser = this;
          ret = llhttp.llhttp_execute(this.ptr, currentBufferPtr, data.length);
        } catch (err) {
          throw err;
        } finally {
          currentParser = null;
          currentBufferRef = null;
        }
        const offset = llhttp.llhttp_get_error_pos(this.ptr) - currentBufferPtr;
        if (ret === constants.ERROR.PAUSED_UPGRADE) {
          this.onUpgrade(data.slice(offset));
        } else if (ret === constants.ERROR.PAUSED) {
          this.paused = true;
          socket.unshift(data.slice(offset));
        } else if (ret !== constants.ERROR.OK) {
          const ptr = llhttp.llhttp_get_error_reason(this.ptr);
          let message = "";
          if (ptr) {
            const len = new Uint8Array(llhttp.memory.buffer, ptr).indexOf(0);
            message = "Response does not match the HTTP/1.1 protocol (" + Buffer.from(llhttp.memory.buffer, ptr, len).toString() + ")";
          }
          throw new HTTPParserError(message, constants.ERROR[ret], data.slice(offset));
        }
      } catch (err) {
        util.destroy(socket, err);
      }
    }
    destroy() {
      assert(this.ptr != null);
      assert(currentParser == null);
      this.llhttp.llhttp_free(this.ptr);
      this.ptr = null;
      this.timeout && timers.clearTimeout(this.timeout);
      this.timeout = null;
      this.timeoutValue = null;
      this.timeoutType = null;
      this.paused = false;
    }
    onStatus(buf) {
      this.statusText = buf.toString();
    }
    onMessageBegin() {
      const { socket, client } = this;
      if (socket.destroyed) {
        return -1;
      }
      const request = client[kQueue][client[kRunningIdx]];
      if (!request) {
        return -1;
      }
      request.onResponseStarted();
    }
    onHeaderField(buf) {
      const len = this.headers.length;
      if ((len & 1) === 0) {
        this.headers.push(buf);
      } else {
        this.headers[len - 1] = Buffer.concat([this.headers[len - 1], buf]);
      }
      this.trackHeader(buf.length);
    }
    onHeaderValue(buf) {
      let len = this.headers.length;
      if ((len & 1) === 1) {
        this.headers.push(buf);
        len += 1;
      } else {
        this.headers[len - 1] = Buffer.concat([this.headers[len - 1], buf]);
      }
      const key = this.headers[len - 2];
      if (key.length === 10) {
        const headerName = util.bufferToLowerCasedHeaderName(key);
        if (headerName === "keep-alive") {
          this.keepAlive += buf.toString();
        } else if (headerName === "connection") {
          this.connection += buf.toString();
        }
      } else if (key.length === 14 && util.bufferToLowerCasedHeaderName(key) === "content-length") {
        this.contentLength += buf.toString();
      }
      this.trackHeader(buf.length);
    }
    trackHeader(len) {
      this.headersSize += len;
      if (this.headersSize >= this.headersMaxSize) {
        util.destroy(this.socket, new HeadersOverflowError);
      }
    }
    onUpgrade(head) {
      const { upgrade, client, socket, headers, statusCode } = this;
      assert(upgrade);
      assert(client[kSocket] === socket);
      assert(!socket.destroyed);
      assert(!this.paused);
      assert((headers.length & 1) === 0);
      const request = client[kQueue][client[kRunningIdx]];
      assert(request);
      assert(request.upgrade || request.method === "CONNECT");
      this.statusCode = null;
      this.statusText = "";
      this.shouldKeepAlive = null;
      this.headers = [];
      this.headersSize = 0;
      socket.unshift(head);
      socket[kParser].destroy();
      socket[kParser] = null;
      socket[kClient] = null;
      socket[kError] = null;
      removeAllListeners(socket);
      client[kSocket] = null;
      client[kHTTPContext] = null;
      client[kQueue][client[kRunningIdx]++] = null;
      client.emit("disconnect", client[kUrl], [client], new InformationalError("upgrade"));
      try {
        request.onUpgrade(statusCode, headers, socket);
      } catch (err) {
        util.destroy(socket, err);
      }
      client[kResume]();
    }
    onHeadersComplete(statusCode, upgrade, shouldKeepAlive) {
      const { client, socket, headers, statusText } = this;
      if (socket.destroyed) {
        return -1;
      }
      const request = client[kQueue][client[kRunningIdx]];
      if (!request) {
        return -1;
      }
      assert(!this.upgrade);
      assert(this.statusCode < 200);
      if (statusCode === 100) {
        util.destroy(socket, new SocketError("bad response", util.getSocketInfo(socket)));
        return -1;
      }
      if (upgrade && !request.upgrade) {
        util.destroy(socket, new SocketError("bad upgrade", util.getSocketInfo(socket)));
        return -1;
      }
      assert(this.timeoutType === TIMEOUT_HEADERS);
      this.statusCode = statusCode;
      this.shouldKeepAlive = shouldKeepAlive || request.method === "HEAD" && !socket[kReset] && this.connection.toLowerCase() === "keep-alive";
      if (this.statusCode >= 200) {
        const bodyTimeout = request.bodyTimeout != null ? request.bodyTimeout : client[kBodyTimeout];
        this.setTimeout(bodyTimeout, TIMEOUT_BODY);
      } else if (this.timeout) {
        if (this.timeout.refresh) {
          this.timeout.refresh();
        }
      }
      if (request.method === "CONNECT") {
        assert(client[kRunning] === 1);
        this.upgrade = true;
        return 2;
      }
      if (upgrade) {
        assert(client[kRunning] === 1);
        this.upgrade = true;
        return 2;
      }
      assert((this.headers.length & 1) === 0);
      this.headers = [];
      this.headersSize = 0;
      if (this.shouldKeepAlive && client[kPipelining]) {
        const keepAliveTimeout = this.keepAlive ? util.parseKeepAliveTimeout(this.keepAlive) : null;
        if (keepAliveTimeout != null) {
          const timeout = Math.min(keepAliveTimeout - client[kKeepAliveTimeoutThreshold], client[kKeepAliveMaxTimeout]);
          if (timeout <= 0) {
            socket[kReset] = true;
          } else {
            client[kKeepAliveTimeoutValue] = timeout;
          }
        } else {
          client[kKeepAliveTimeoutValue] = client[kKeepAliveDefaultTimeout];
        }
      } else {
        socket[kReset] = true;
      }
      const pause = request.onHeaders(statusCode, headers, this.resume, statusText) === false;
      if (request.aborted) {
        return -1;
      }
      if (request.method === "HEAD") {
        return 1;
      }
      if (statusCode < 200) {
        return 1;
      }
      if (socket[kBlocking]) {
        socket[kBlocking] = false;
        client[kResume]();
      }
      return pause ? constants.ERROR.PAUSED : 0;
    }
    onBody(buf) {
      const { client, socket, statusCode, maxResponseSize } = this;
      if (socket.destroyed) {
        return -1;
      }
      const request = client[kQueue][client[kRunningIdx]];
      assert(request);
      assert(this.timeoutType === TIMEOUT_BODY);
      if (this.timeout) {
        if (this.timeout.refresh) {
          this.timeout.refresh();
        }
      }
      assert(statusCode >= 200);
      if (maxResponseSize > -1 && this.bytesRead + buf.length > maxResponseSize) {
        util.destroy(socket, new ResponseExceededMaxSizeError);
        return -1;
      }
      this.bytesRead += buf.length;
      if (request.onData(buf) === false) {
        return constants.ERROR.PAUSED;
      }
    }
    onMessageComplete() {
      const { client, socket, statusCode, upgrade, headers, contentLength, bytesRead, shouldKeepAlive } = this;
      if (socket.destroyed && (!statusCode || shouldKeepAlive)) {
        return -1;
      }
      if (upgrade) {
        return;
      }
      assert(statusCode >= 100);
      assert((this.headers.length & 1) === 0);
      const request = client[kQueue][client[kRunningIdx]];
      assert(request);
      this.statusCode = null;
      this.statusText = "";
      this.bytesRead = 0;
      this.contentLength = "";
      this.keepAlive = "";
      this.connection = "";
      this.headers = [];
      this.headersSize = 0;
      if (statusCode < 200) {
        return;
      }
      if (request.method !== "HEAD" && contentLength && bytesRead !== parseInt(contentLength, 10)) {
        util.destroy(socket, new ResponseContentLengthMismatchError);
        return -1;
      }
      request.onComplete(headers);
      client[kQueue][client[kRunningIdx]++] = null;
      if (socket[kWriting]) {
        assert(client[kRunning] === 0);
        util.destroy(socket, new InformationalError("reset"));
        return constants.ERROR.PAUSED;
      } else if (!shouldKeepAlive) {
        util.destroy(socket, new InformationalError("reset"));
        return constants.ERROR.PAUSED;
      } else if (socket[kReset] && client[kRunning] === 0) {
        util.destroy(socket, new InformationalError("reset"));
        return constants.ERROR.PAUSED;
      } else if (client[kPipelining] == null || client[kPipelining] === 1) {
        setImmediate(() => client[kResume]());
      } else {
        client[kResume]();
      }
    }
  }
  function onParserTimeout(parser) {
    const { socket, timeoutType, client, paused } = parser.deref();
    if (timeoutType === TIMEOUT_HEADERS) {
      if (!socket[kWriting] || socket.writableNeedDrain || client[kRunning] > 1) {
        assert(!paused, "cannot be paused while waiting for headers");
        util.destroy(socket, new HeadersTimeoutError);
      }
    } else if (timeoutType === TIMEOUT_BODY) {
      if (!paused) {
        util.destroy(socket, new BodyTimeoutError);
      }
    } else if (timeoutType === TIMEOUT_KEEP_ALIVE) {
      assert(client[kRunning] === 0 && client[kKeepAliveTimeoutValue]);
      util.destroy(socket, new InformationalError("socket idle timeout"));
    }
  }
  async function connectH1(client, socket) {
    client[kSocket] = socket;
    if (!llhttpInstance) {
      llhttpInstance = await llhttpPromise;
      llhttpPromise = null;
    }
    socket[kNoRef] = false;
    socket[kWriting] = false;
    socket[kReset] = false;
    socket[kBlocking] = false;
    socket[kParser] = new Parser(client, socket, llhttpInstance);
    addListener(socket, "error", function(err) {
      assert(err.code !== "ERR_TLS_CERT_ALTNAME_INVALID");
      const parser = this[kParser];
      if (err.code === "ECONNRESET" && parser.statusCode && !parser.shouldKeepAlive) {
        parser.onMessageComplete();
        return;
      }
      this[kError] = err;
      this[kClient][kOnError](err);
    });
    addListener(socket, "readable", function() {
      const parser = this[kParser];
      if (parser) {
        parser.readMore();
      }
    });
    addListener(socket, "end", function() {
      const parser = this[kParser];
      if (parser.statusCode && !parser.shouldKeepAlive) {
        parser.onMessageComplete();
        return;
      }
      util.destroy(this, new SocketError("other side closed", util.getSocketInfo(this)));
    });
    addListener(socket, "close", function() {
      const client2 = this[kClient];
      const parser = this[kParser];
      if (parser) {
        if (!this[kError] && parser.statusCode && !parser.shouldKeepAlive) {
          parser.onMessageComplete();
        }
        this[kParser].destroy();
        this[kParser] = null;
      }
      const err = this[kError] || new SocketError("closed", util.getSocketInfo(this));
      client2[kSocket] = null;
      client2[kHTTPContext] = null;
      if (client2.destroyed) {
        assert(client2[kPending] === 0);
        const requests = client2[kQueue].splice(client2[kRunningIdx]);
        for (let i = 0;i < requests.length; i++) {
          const request = requests[i];
          util.errorRequest(client2, request, err);
        }
      } else if (client2[kRunning] > 0 && err.code !== "UND_ERR_INFO") {
        const request = client2[kQueue][client2[kRunningIdx]];
        client2[kQueue][client2[kRunningIdx]++] = null;
        util.errorRequest(client2, request, err);
      }
      client2[kPendingIdx] = client2[kRunningIdx];
      assert(client2[kRunning] === 0);
      client2.emit("disconnect", client2[kUrl], [client2], err);
      client2[kResume]();
    });
    let closed = false;
    socket.on("close", () => {
      closed = true;
    });
    return {
      version: "h1",
      defaultPipelining: 1,
      write(...args) {
        return writeH1(client, ...args);
      },
      resume() {
        resumeH1(client);
      },
      destroy(err, callback) {
        if (closed) {
          queueMicrotask(callback);
        } else {
          socket.destroy(err).on("close", callback);
        }
      },
      get destroyed() {
        return socket.destroyed;
      },
      busy(request) {
        if (socket[kWriting] || socket[kReset] || socket[kBlocking]) {
          return true;
        }
        if (request) {
          if (client[kRunning] > 0 && !request.idempotent) {
            return true;
          }
          if (client[kRunning] > 0 && (request.upgrade || request.method === "CONNECT")) {
            return true;
          }
          if (client[kRunning] > 0 && util.bodyLength(request.body) !== 0 && (util.isStream(request.body) || util.isAsyncIterable(request.body) || util.isFormDataLike(request.body))) {
            return true;
          }
        }
        return false;
      }
    };
  }
  function resumeH1(client) {
    const socket = client[kSocket];
    if (socket && !socket.destroyed) {
      if (client[kSize] === 0) {
        if (!socket[kNoRef] && socket.unref) {
          socket.unref();
          socket[kNoRef] = true;
        }
      } else if (socket[kNoRef] && socket.ref) {
        socket.ref();
        socket[kNoRef] = false;
      }
      if (client[kSize] === 0) {
        if (socket[kParser].timeoutType !== TIMEOUT_KEEP_ALIVE) {
          socket[kParser].setTimeout(client[kKeepAliveTimeoutValue], TIMEOUT_KEEP_ALIVE);
        }
      } else if (client[kRunning] > 0 && socket[kParser].statusCode < 200) {
        if (socket[kParser].timeoutType !== TIMEOUT_HEADERS) {
          const request = client[kQueue][client[kRunningIdx]];
          const headersTimeout = request.headersTimeout != null ? request.headersTimeout : client[kHeadersTimeout];
          socket[kParser].setTimeout(headersTimeout, TIMEOUT_HEADERS);
        }
      }
    }
  }
  function shouldSendContentLength(method) {
    return method !== "GET" && method !== "HEAD" && method !== "OPTIONS" && method !== "TRACE" && method !== "CONNECT";
  }
  function writeH1(client, request) {
    const { method, path, host, upgrade, blocking, reset } = request;
    let { body, headers, contentLength } = request;
    const expectsPayload = method === "PUT" || method === "POST" || method === "PATCH" || method === "QUERY" || method === "PROPFIND" || method === "PROPPATCH";
    if (util.isFormDataLike(body)) {
      if (!extractBody) {
        extractBody = require_body().extractBody;
      }
      const [bodyStream, contentType] = extractBody(body);
      if (request.contentType == null) {
        headers.push("content-type", contentType);
      }
      body = bodyStream.stream;
      contentLength = bodyStream.length;
    } else if (util.isBlobLike(body) && request.contentType == null && body.type) {
      headers.push("content-type", body.type);
    }
    if (body && typeof body.read === "function") {
      body.read(0);
    }
    const bodyLength = util.bodyLength(body);
    contentLength = bodyLength ?? contentLength;
    if (contentLength === null) {
      contentLength = request.contentLength;
    }
    if (contentLength === 0 && !expectsPayload) {
      contentLength = null;
    }
    if (shouldSendContentLength(method) && contentLength > 0 && request.contentLength !== null && request.contentLength !== contentLength) {
      if (client[kStrictContentLength]) {
        util.errorRequest(client, request, new RequestContentLengthMismatchError);
        return false;
      }
      process.emitWarning(new RequestContentLengthMismatchError);
    }
    const socket = client[kSocket];
    const abort = (err) => {
      if (request.aborted || request.completed) {
        return;
      }
      util.errorRequest(client, request, err || new RequestAbortedError);
      util.destroy(body);
      util.destroy(socket, new InformationalError("aborted"));
    };
    try {
      request.onConnect(abort);
    } catch (err) {
      util.errorRequest(client, request, err);
    }
    if (request.aborted) {
      return false;
    }
    if (method === "HEAD") {
      socket[kReset] = true;
    }
    if (upgrade || method === "CONNECT") {
      socket[kReset] = true;
    }
    if (reset != null) {
      socket[kReset] = reset;
    }
    if (client[kMaxRequests] && socket[kCounter]++ >= client[kMaxRequests]) {
      socket[kReset] = true;
    }
    if (blocking) {
      socket[kBlocking] = true;
    }
    let header = `${method} ${path} HTTP/1.1\r
`;
    if (typeof host === "string") {
      header += `host: ${host}\r
`;
    } else {
      header += client[kHostHeader];
    }
    if (upgrade) {
      header += `connection: upgrade\r
upgrade: ${upgrade}\r
`;
    } else if (client[kPipelining] && !socket[kReset]) {
      header += `connection: keep-alive\r
`;
    } else {
      header += `connection: close\r
`;
    }
    if (Array.isArray(headers)) {
      for (let n = 0;n < headers.length; n += 2) {
        const key = headers[n + 0];
        const val = headers[n + 1];
        if (Array.isArray(val)) {
          for (let i = 0;i < val.length; i++) {
            header += `${key}: ${val[i]}\r
`;
          }
        } else {
          header += `${key}: ${val}\r
`;
        }
      }
    }
    if (channels.sendHeaders.hasSubscribers) {
      channels.sendHeaders.publish({ request, headers: header, socket });
    }
    if (!body || bodyLength === 0) {
      writeBuffer(abort, null, client, request, socket, contentLength, header, expectsPayload);
    } else if (util.isBuffer(body)) {
      writeBuffer(abort, body, client, request, socket, contentLength, header, expectsPayload);
    } else if (util.isBlobLike(body)) {
      if (typeof body.stream === "function") {
        writeIterable(abort, body.stream(), client, request, socket, contentLength, header, expectsPayload);
      } else {
        writeBlob(abort, body, client, request, socket, contentLength, header, expectsPayload);
      }
    } else if (util.isStream(body)) {
      writeStream(abort, body, client, request, socket, contentLength, header, expectsPayload);
    } else if (util.isIterable(body)) {
      writeIterable(abort, body, client, request, socket, contentLength, header, expectsPayload);
    } else {
      assert(false);
    }
    return true;
  }
  function writeStream(abort, body, client, request, socket, contentLength, header, expectsPayload) {
    assert(contentLength !== 0 || client[kRunning] === 0, "stream body cannot be pipelined");
    let finished = false;
    const writer = new AsyncWriter({ abort, socket, request, contentLength, client, expectsPayload, header });
    const onData = function(chunk) {
      if (finished) {
        return;
      }
      try {
        if (!writer.write(chunk) && this.pause) {
          this.pause();
        }
      } catch (err) {
        util.destroy(this, err);
      }
    };
    const onDrain = function() {
      if (finished) {
        return;
      }
      if (body.resume) {
        body.resume();
      }
    };
    const onClose = function() {
      queueMicrotask(() => {
        body.removeListener("error", onFinished);
      });
      if (!finished) {
        const err = new RequestAbortedError;
        queueMicrotask(() => onFinished(err));
      }
    };
    const onFinished = function(err) {
      if (finished) {
        return;
      }
      finished = true;
      assert(socket.destroyed || socket[kWriting] && client[kRunning] <= 1);
      socket.off("drain", onDrain).off("error", onFinished);
      body.removeListener("data", onData).removeListener("end", onFinished).removeListener("close", onClose);
      if (!err) {
        try {
          writer.end();
        } catch (er) {
          err = er;
        }
      }
      writer.destroy(err);
      if (err && (err.code !== "UND_ERR_INFO" || err.message !== "reset")) {
        util.destroy(body, err);
      } else {
        util.destroy(body);
      }
    };
    body.on("data", onData).on("end", onFinished).on("error", onFinished).on("close", onClose);
    if (body.resume) {
      body.resume();
    }
    socket.on("drain", onDrain).on("error", onFinished);
    if (body.errorEmitted ?? body.errored) {
      setImmediate(() => onFinished(body.errored));
    } else if (body.endEmitted ?? body.readableEnded) {
      setImmediate(() => onFinished(null));
    }
    if (body.closeEmitted ?? body.closed) {
      setImmediate(onClose);
    }
  }
  function writeBuffer(abort, body, client, request, socket, contentLength, header, expectsPayload) {
    try {
      if (!body) {
        if (contentLength === 0) {
          socket.write(`${header}content-length: 0\r
\r
`, "latin1");
        } else {
          assert(contentLength === null, "no body must not have content length");
          socket.write(`${header}\r
`, "latin1");
        }
      } else if (util.isBuffer(body)) {
        assert(contentLength === body.byteLength, "buffer body must have content length");
        socket.cork();
        socket.write(`${header}content-length: ${contentLength}\r
\r
`, "latin1");
        socket.write(body);
        socket.uncork();
        request.onBodySent(body);
        if (!expectsPayload && request.reset !== false) {
          socket[kReset] = true;
        }
      }
      request.onRequestSent();
      client[kResume]();
    } catch (err) {
      abort(err);
    }
  }
  async function writeBlob(abort, body, client, request, socket, contentLength, header, expectsPayload) {
    assert(contentLength === body.size, "blob body must have content length");
    try {
      if (contentLength != null && contentLength !== body.size) {
        throw new RequestContentLengthMismatchError;
      }
      const buffer = Buffer.from(await body.arrayBuffer());
      socket.cork();
      socket.write(`${header}content-length: ${contentLength}\r
\r
`, "latin1");
      socket.write(buffer);
      socket.uncork();
      request.onBodySent(buffer);
      request.onRequestSent();
      if (!expectsPayload && request.reset !== false) {
        socket[kReset] = true;
      }
      client[kResume]();
    } catch (err) {
      abort(err);
    }
  }
  async function writeIterable(abort, body, client, request, socket, contentLength, header, expectsPayload) {
    assert(contentLength !== 0 || client[kRunning] === 0, "iterator body cannot be pipelined");
    let callback = null;
    function onDrain() {
      if (callback) {
        const cb = callback;
        callback = null;
        cb();
      }
    }
    const waitForDrain = () => new Promise((resolve3, reject) => {
      assert(callback === null);
      if (socket[kError]) {
        reject(socket[kError]);
      } else {
        callback = resolve3;
      }
    });
    socket.on("close", onDrain).on("drain", onDrain);
    const writer = new AsyncWriter({ abort, socket, request, contentLength, client, expectsPayload, header });
    try {
      for await (const chunk of body) {
        if (socket[kError]) {
          throw socket[kError];
        }
        if (!writer.write(chunk)) {
          await waitForDrain();
        }
      }
      writer.end();
    } catch (err) {
      writer.destroy(err);
    } finally {
      socket.off("close", onDrain).off("drain", onDrain);
    }
  }

  class AsyncWriter {
    constructor({ abort, socket, request, contentLength, client, expectsPayload, header }) {
      this.socket = socket;
      this.request = request;
      this.contentLength = contentLength;
      this.client = client;
      this.bytesWritten = 0;
      this.expectsPayload = expectsPayload;
      this.header = header;
      this.abort = abort;
      socket[kWriting] = true;
    }
    write(chunk) {
      const { socket, request, contentLength, client, bytesWritten, expectsPayload, header } = this;
      if (socket[kError]) {
        throw socket[kError];
      }
      if (socket.destroyed) {
        return false;
      }
      const len = Buffer.byteLength(chunk);
      if (!len) {
        return true;
      }
      if (contentLength !== null && bytesWritten + len > contentLength) {
        if (client[kStrictContentLength]) {
          throw new RequestContentLengthMismatchError;
        }
        process.emitWarning(new RequestContentLengthMismatchError);
      }
      socket.cork();
      if (bytesWritten === 0) {
        if (!expectsPayload && request.reset !== false) {
          socket[kReset] = true;
        }
        if (contentLength === null) {
          socket.write(`${header}transfer-encoding: chunked\r
`, "latin1");
        } else {
          socket.write(`${header}content-length: ${contentLength}\r
\r
`, "latin1");
        }
      }
      if (contentLength === null) {
        socket.write(`\r
${len.toString(16)}\r
`, "latin1");
      }
      this.bytesWritten += len;
      const ret = socket.write(chunk);
      socket.uncork();
      request.onBodySent(chunk);
      if (!ret) {
        if (socket[kParser].timeout && socket[kParser].timeoutType === TIMEOUT_HEADERS) {
          if (socket[kParser].timeout.refresh) {
            socket[kParser].timeout.refresh();
          }
        }
      }
      return ret;
    }
    end() {
      const { socket, contentLength, client, bytesWritten, expectsPayload, header, request } = this;
      request.onRequestSent();
      socket[kWriting] = false;
      if (socket[kError]) {
        throw socket[kError];
      }
      if (socket.destroyed) {
        return;
      }
      if (bytesWritten === 0) {
        if (expectsPayload) {
          socket.write(`${header}content-length: 0\r
\r
`, "latin1");
        } else {
          socket.write(`${header}\r
`, "latin1");
        }
      } else if (contentLength === null) {
        socket.write(`\r
0\r
\r
`, "latin1");
      }
      if (contentLength !== null && bytesWritten !== contentLength) {
        if (client[kStrictContentLength]) {
          throw new RequestContentLengthMismatchError;
        } else {
          process.emitWarning(new RequestContentLengthMismatchError);
        }
      }
      if (socket[kParser].timeout && socket[kParser].timeoutType === TIMEOUT_HEADERS) {
        if (socket[kParser].timeout.refresh) {
          socket[kParser].timeout.refresh();
        }
      }
      client[kResume]();
    }
    destroy(err) {
      const { socket, client, abort } = this;
      socket[kWriting] = false;
      if (err) {
        assert(client[kRunning] <= 1, "pipeline should only contain this request");
        abort(err);
      }
    }
  }
  module.exports = connectH1;
});

// node_modules/undici/lib/dispatcher/client-h2.js
var require_client_h2 = __commonJS((exports, module) => {
  var assert = __require("assert");
  var { pipeline } = __require("stream");
  var util = require_util();
  var {
    RequestContentLengthMismatchError,
    RequestAbortedError,
    SocketError,
    InformationalError
  } = require_errors();
  var {
    kUrl,
    kReset,
    kClient,
    kRunning,
    kPending,
    kQueue,
    kPendingIdx,
    kRunningIdx,
    kError,
    kSocket,
    kStrictContentLength,
    kOnError,
    kMaxConcurrentStreams,
    kHTTP2Session,
    kResume,
    kSize,
    kHTTPContext
  } = require_symbols();
  var kOpenStreams = Symbol("open streams");
  var extractBody;
  var h2ExperimentalWarned = false;
  var http2;
  try {
    http2 = __require("http2");
  } catch {
    http2 = { constants: {} };
  }
  var {
    constants: {
      HTTP2_HEADER_AUTHORITY,
      HTTP2_HEADER_METHOD,
      HTTP2_HEADER_PATH,
      HTTP2_HEADER_SCHEME,
      HTTP2_HEADER_CONTENT_LENGTH,
      HTTP2_HEADER_EXPECT,
      HTTP2_HEADER_STATUS
    }
  } = http2;
  function parseH2Headers(headers) {
    const result = [];
    for (const [name, value] of Object.entries(headers)) {
      if (Array.isArray(value)) {
        for (const subvalue of value) {
          result.push(Buffer.from(name), Buffer.from(subvalue));
        }
      } else {
        result.push(Buffer.from(name), Buffer.from(value));
      }
    }
    return result;
  }
  async function connectH2(client, socket) {
    client[kSocket] = socket;
    if (!h2ExperimentalWarned) {
      h2ExperimentalWarned = true;
      process.emitWarning("H2 support is experimental, expect them to change at any time.", {
        code: "UNDICI-H2"
      });
    }
    const session = http2.connect(client[kUrl], {
      createConnection: () => socket,
      peerMaxConcurrentStreams: client[kMaxConcurrentStreams]
    });
    session[kOpenStreams] = 0;
    session[kClient] = client;
    session[kSocket] = socket;
    util.addListener(session, "error", onHttp2SessionError);
    util.addListener(session, "frameError", onHttp2FrameError);
    util.addListener(session, "end", onHttp2SessionEnd);
    util.addListener(session, "goaway", onHTTP2GoAway);
    util.addListener(session, "close", function() {
      const { [kClient]: client2 } = this;
      const { [kSocket]: socket2 } = client2;
      const err = this[kSocket][kError] || this[kError] || new SocketError("closed", util.getSocketInfo(socket2));
      client2[kHTTP2Session] = null;
      if (client2.destroyed) {
        assert(client2[kPending] === 0);
        const requests = client2[kQueue].splice(client2[kRunningIdx]);
        for (let i = 0;i < requests.length; i++) {
          const request = requests[i];
          util.errorRequest(client2, request, err);
        }
      }
    });
    session.unref();
    client[kHTTP2Session] = session;
    socket[kHTTP2Session] = session;
    util.addListener(socket, "error", function(err) {
      assert(err.code !== "ERR_TLS_CERT_ALTNAME_INVALID");
      this[kError] = err;
      this[kClient][kOnError](err);
    });
    util.addListener(socket, "end", function() {
      util.destroy(this, new SocketError("other side closed", util.getSocketInfo(this)));
    });
    util.addListener(socket, "close", function() {
      const err = this[kError] || new SocketError("closed", util.getSocketInfo(this));
      client[kSocket] = null;
      if (this[kHTTP2Session] != null) {
        this[kHTTP2Session].destroy(err);
      }
      client[kPendingIdx] = client[kRunningIdx];
      assert(client[kRunning] === 0);
      client.emit("disconnect", client[kUrl], [client], err);
      client[kResume]();
    });
    let closed = false;
    socket.on("close", () => {
      closed = true;
    });
    return {
      version: "h2",
      defaultPipelining: Infinity,
      write(...args) {
        return writeH2(client, ...args);
      },
      resume() {
        resumeH2(client);
      },
      destroy(err, callback) {
        if (closed) {
          queueMicrotask(callback);
        } else {
          socket.destroy(err).on("close", callback);
        }
      },
      get destroyed() {
        return socket.destroyed;
      },
      busy() {
        return false;
      }
    };
  }
  function resumeH2(client) {
    const socket = client[kSocket];
    if (socket?.destroyed === false) {
      if (client[kSize] === 0 && client[kMaxConcurrentStreams] === 0) {
        socket.unref();
        client[kHTTP2Session].unref();
      } else {
        socket.ref();
        client[kHTTP2Session].ref();
      }
    }
  }
  function onHttp2SessionError(err) {
    assert(err.code !== "ERR_TLS_CERT_ALTNAME_INVALID");
    this[kSocket][kError] = err;
    this[kClient][kOnError](err);
  }
  function onHttp2FrameError(type, code, id) {
    if (id === 0) {
      const err = new InformationalError(`HTTP/2: "frameError" received - type ${type}, code ${code}`);
      this[kSocket][kError] = err;
      this[kClient][kOnError](err);
    }
  }
  function onHttp2SessionEnd() {
    const err = new SocketError("other side closed", util.getSocketInfo(this[kSocket]));
    this.destroy(err);
    util.destroy(this[kSocket], err);
  }
  function onHTTP2GoAway(code) {
    const err = this[kError] || new SocketError(`HTTP/2: "GOAWAY" frame received with code ${code}`, util.getSocketInfo(this));
    const client = this[kClient];
    client[kSocket] = null;
    client[kHTTPContext] = null;
    if (this[kHTTP2Session] != null) {
      this[kHTTP2Session].destroy(err);
      this[kHTTP2Session] = null;
    }
    util.destroy(this[kSocket], err);
    if (client[kRunningIdx] < client[kQueue].length) {
      const request = client[kQueue][client[kRunningIdx]];
      client[kQueue][client[kRunningIdx]++] = null;
      util.errorRequest(client, request, err);
      client[kPendingIdx] = client[kRunningIdx];
    }
    assert(client[kRunning] === 0);
    client.emit("disconnect", client[kUrl], [client], err);
    client[kResume]();
  }
  function shouldSendContentLength(method) {
    return method !== "GET" && method !== "HEAD" && method !== "OPTIONS" && method !== "TRACE" && method !== "CONNECT";
  }
  function writeH2(client, request) {
    const session = client[kHTTP2Session];
    const { method, path, host, upgrade, expectContinue, signal, headers: reqHeaders } = request;
    let { body } = request;
    if (upgrade) {
      util.errorRequest(client, request, new Error("Upgrade not supported for H2"));
      return false;
    }
    const headers = {};
    for (let n = 0;n < reqHeaders.length; n += 2) {
      const key = reqHeaders[n + 0];
      const val = reqHeaders[n + 1];
      if (Array.isArray(val)) {
        for (let i = 0;i < val.length; i++) {
          if (headers[key]) {
            headers[key] += `,${val[i]}`;
          } else {
            headers[key] = val[i];
          }
        }
      } else {
        headers[key] = val;
      }
    }
    let stream;
    const { hostname, port } = client[kUrl];
    headers[HTTP2_HEADER_AUTHORITY] = host || `${hostname}${port ? `:${port}` : ""}`;
    headers[HTTP2_HEADER_METHOD] = method;
    const abort = (err) => {
      if (request.aborted || request.completed) {
        return;
      }
      err = err || new RequestAbortedError;
      util.errorRequest(client, request, err);
      if (stream != null) {
        util.destroy(stream, err);
      }
      util.destroy(body, err);
      client[kQueue][client[kRunningIdx]++] = null;
      client[kResume]();
    };
    try {
      request.onConnect(abort);
    } catch (err) {
      util.errorRequest(client, request, err);
    }
    if (request.aborted) {
      return false;
    }
    if (method === "CONNECT") {
      session.ref();
      stream = session.request(headers, { endStream: false, signal });
      if (stream.id && !stream.pending) {
        request.onUpgrade(null, null, stream);
        ++session[kOpenStreams];
        client[kQueue][client[kRunningIdx]++] = null;
      } else {
        stream.once("ready", () => {
          request.onUpgrade(null, null, stream);
          ++session[kOpenStreams];
          client[kQueue][client[kRunningIdx]++] = null;
        });
      }
      stream.once("close", () => {
        session[kOpenStreams] -= 1;
        if (session[kOpenStreams] === 0)
          session.unref();
      });
      return true;
    }
    headers[HTTP2_HEADER_PATH] = path;
    headers[HTTP2_HEADER_SCHEME] = "https";
    const expectsPayload = method === "PUT" || method === "POST" || method === "PATCH";
    if (body && typeof body.read === "function") {
      body.read(0);
    }
    let contentLength = util.bodyLength(body);
    if (util.isFormDataLike(body)) {
      extractBody ??= require_body().extractBody;
      const [bodyStream, contentType] = extractBody(body);
      headers["content-type"] = contentType;
      body = bodyStream.stream;
      contentLength = bodyStream.length;
    }
    if (contentLength == null) {
      contentLength = request.contentLength;
    }
    if (contentLength === 0 || !expectsPayload) {
      contentLength = null;
    }
    if (shouldSendContentLength(method) && contentLength > 0 && request.contentLength != null && request.contentLength !== contentLength) {
      if (client[kStrictContentLength]) {
        util.errorRequest(client, request, new RequestContentLengthMismatchError);
        return false;
      }
      process.emitWarning(new RequestContentLengthMismatchError);
    }
    if (contentLength != null) {
      assert(body, "no body must not have content length");
      headers[HTTP2_HEADER_CONTENT_LENGTH] = `${contentLength}`;
    }
    session.ref();
    const shouldEndStream = method === "GET" || method === "HEAD" || body === null;
    if (expectContinue) {
      headers[HTTP2_HEADER_EXPECT] = "100-continue";
      stream = session.request(headers, { endStream: shouldEndStream, signal });
      stream.once("continue", writeBodyH2);
    } else {
      stream = session.request(headers, {
        endStream: shouldEndStream,
        signal
      });
      writeBodyH2();
    }
    ++session[kOpenStreams];
    stream.once("response", (headers2) => {
      const { [HTTP2_HEADER_STATUS]: statusCode, ...realHeaders } = headers2;
      request.onResponseStarted();
      if (request.aborted) {
        const err = new RequestAbortedError;
        util.errorRequest(client, request, err);
        util.destroy(stream, err);
        return;
      }
      if (request.onHeaders(Number(statusCode), parseH2Headers(realHeaders), stream.resume.bind(stream), "") === false) {
        stream.pause();
      }
      stream.on("data", (chunk) => {
        if (request.onData(chunk) === false) {
          stream.pause();
        }
      });
    });
    stream.once("end", () => {
      if (stream.state?.state == null || stream.state.state < 6) {
        request.onComplete([]);
      }
      if (session[kOpenStreams] === 0) {
        session.unref();
      }
      abort(new InformationalError("HTTP/2: stream half-closed (remote)"));
      client[kQueue][client[kRunningIdx]++] = null;
      client[kPendingIdx] = client[kRunningIdx];
      client[kResume]();
    });
    stream.once("close", () => {
      session[kOpenStreams] -= 1;
      if (session[kOpenStreams] === 0) {
        session.unref();
      }
    });
    stream.once("error", function(err) {
      abort(err);
    });
    stream.once("frameError", (type, code) => {
      abort(new InformationalError(`HTTP/2: "frameError" received - type ${type}, code ${code}`));
    });
    return true;
    function writeBodyH2() {
      if (!body || contentLength === 0) {
        writeBuffer(abort, stream, null, client, request, client[kSocket], contentLength, expectsPayload);
      } else if (util.isBuffer(body)) {
        writeBuffer(abort, stream, body, client, request, client[kSocket], contentLength, expectsPayload);
      } else if (util.isBlobLike(body)) {
        if (typeof body.stream === "function") {
          writeIterable(abort, stream, body.stream(), client, request, client[kSocket], contentLength, expectsPayload);
        } else {
          writeBlob(abort, stream, body, client, request, client[kSocket], contentLength, expectsPayload);
        }
      } else if (util.isStream(body)) {
        writeStream(abort, client[kSocket], expectsPayload, stream, body, client, request, contentLength);
      } else if (util.isIterable(body)) {
        writeIterable(abort, stream, body, client, request, client[kSocket], contentLength, expectsPayload);
      } else {
        assert(false);
      }
    }
  }
  function writeBuffer(abort, h2stream, body, client, request, socket, contentLength, expectsPayload) {
    try {
      if (body != null && util.isBuffer(body)) {
        assert(contentLength === body.byteLength, "buffer body must have content length");
        h2stream.cork();
        h2stream.write(body);
        h2stream.uncork();
        h2stream.end();
        request.onBodySent(body);
      }
      if (!expectsPayload) {
        socket[kReset] = true;
      }
      request.onRequestSent();
      client[kResume]();
    } catch (error) {
      abort(error);
    }
  }
  function writeStream(abort, socket, expectsPayload, h2stream, body, client, request, contentLength) {
    assert(contentLength !== 0 || client[kRunning] === 0, "stream body cannot be pipelined");
    const pipe = pipeline(body, h2stream, (err) => {
      if (err) {
        util.destroy(pipe, err);
        abort(err);
      } else {
        util.removeAllListeners(pipe);
        request.onRequestSent();
        if (!expectsPayload) {
          socket[kReset] = true;
        }
        client[kResume]();
      }
    });
    util.addListener(pipe, "data", onPipeData);
    function onPipeData(chunk) {
      request.onBodySent(chunk);
    }
  }
  async function writeBlob(abort, h2stream, body, client, request, socket, contentLength, expectsPayload) {
    assert(contentLength === body.size, "blob body must have content length");
    try {
      if (contentLength != null && contentLength !== body.size) {
        throw new RequestContentLengthMismatchError;
      }
      const buffer = Buffer.from(await body.arrayBuffer());
      h2stream.cork();
      h2stream.write(buffer);
      h2stream.uncork();
      h2stream.end();
      request.onBodySent(buffer);
      request.onRequestSent();
      if (!expectsPayload) {
        socket[kReset] = true;
      }
      client[kResume]();
    } catch (err) {
      abort(err);
    }
  }
  async function writeIterable(abort, h2stream, body, client, request, socket, contentLength, expectsPayload) {
    assert(contentLength !== 0 || client[kRunning] === 0, "iterator body cannot be pipelined");
    let callback = null;
    function onDrain() {
      if (callback) {
        const cb = callback;
        callback = null;
        cb();
      }
    }
    const waitForDrain = () => new Promise((resolve3, reject) => {
      assert(callback === null);
      if (socket[kError]) {
        reject(socket[kError]);
      } else {
        callback = resolve3;
      }
    });
    h2stream.on("close", onDrain).on("drain", onDrain);
    try {
      for await (const chunk of body) {
        if (socket[kError]) {
          throw socket[kError];
        }
        const res = h2stream.write(chunk);
        request.onBodySent(chunk);
        if (!res) {
          await waitForDrain();
        }
      }
      h2stream.end();
      request.onRequestSent();
      if (!expectsPayload) {
        socket[kReset] = true;
      }
      client[kResume]();
    } catch (err) {
      abort(err);
    } finally {
      h2stream.off("close", onDrain).off("drain", onDrain);
    }
  }
  module.exports = connectH2;
});

// node_modules/undici/lib/handler/redirect-handler.js
var require_redirect_handler = __commonJS((exports, module) => {
  var util = require_util();
  var { kBodyUsed } = require_symbols();
  var assert = __require("assert");
  var { InvalidArgumentError } = require_errors();
  var EE = __require("events");
  var redirectableStatusCodes = [300, 301, 302, 303, 307, 308];
  var kBody = Symbol("body");

  class BodyAsyncIterable {
    constructor(body) {
      this[kBody] = body;
      this[kBodyUsed] = false;
    }
    async* [Symbol.asyncIterator]() {
      assert(!this[kBodyUsed], "disturbed");
      this[kBodyUsed] = true;
      yield* this[kBody];
    }
  }

  class RedirectHandler {
    constructor(dispatch, maxRedirections, opts, handler) {
      if (maxRedirections != null && (!Number.isInteger(maxRedirections) || maxRedirections < 0)) {
        throw new InvalidArgumentError("maxRedirections must be a positive number");
      }
      util.validateHandler(handler, opts.method, opts.upgrade);
      this.dispatch = dispatch;
      this.location = null;
      this.abort = null;
      this.opts = { ...opts, maxRedirections: 0 };
      this.maxRedirections = maxRedirections;
      this.handler = handler;
      this.history = [];
      this.redirectionLimitReached = false;
      if (util.isStream(this.opts.body)) {
        if (util.bodyLength(this.opts.body) === 0) {
          this.opts.body.on("data", function() {
            assert(false);
          });
        }
        if (typeof this.opts.body.readableDidRead !== "boolean") {
          this.opts.body[kBodyUsed] = false;
          EE.prototype.on.call(this.opts.body, "data", function() {
            this[kBodyUsed] = true;
          });
        }
      } else if (this.opts.body && typeof this.opts.body.pipeTo === "function") {
        this.opts.body = new BodyAsyncIterable(this.opts.body);
      } else if (this.opts.body && typeof this.opts.body !== "string" && !ArrayBuffer.isView(this.opts.body) && util.isIterable(this.opts.body)) {
        this.opts.body = new BodyAsyncIterable(this.opts.body);
      }
    }
    onConnect(abort) {
      this.abort = abort;
      this.handler.onConnect(abort, { history: this.history });
    }
    onUpgrade(statusCode, headers, socket) {
      this.handler.onUpgrade(statusCode, headers, socket);
    }
    onError(error) {
      this.handler.onError(error);
    }
    onHeaders(statusCode, headers, resume, statusText) {
      this.location = this.history.length >= this.maxRedirections || util.isDisturbed(this.opts.body) ? null : parseLocation(statusCode, headers);
      if (this.opts.throwOnMaxRedirect && this.history.length >= this.maxRedirections) {
        if (this.request) {
          this.request.abort(new Error("max redirects"));
        }
        this.redirectionLimitReached = true;
        this.abort(new Error("max redirects"));
        return;
      }
      if (this.opts.origin) {
        this.history.push(new URL(this.opts.path, this.opts.origin));
      }
      if (!this.location) {
        return this.handler.onHeaders(statusCode, headers, resume, statusText);
      }
      const { origin, pathname, search } = util.parseURL(new URL(this.location, this.opts.origin && new URL(this.opts.path, this.opts.origin)));
      const path = search ? `${pathname}${search}` : pathname;
      this.opts.headers = cleanRequestHeaders(this.opts.headers, statusCode === 303, this.opts.origin !== origin);
      this.opts.path = path;
      this.opts.origin = origin;
      this.opts.maxRedirections = 0;
      this.opts.query = null;
      if (statusCode === 303 && this.opts.method !== "HEAD") {
        this.opts.method = "GET";
        this.opts.body = null;
      }
    }
    onData(chunk) {
      if (this.location) {} else {
        return this.handler.onData(chunk);
      }
    }
    onComplete(trailers) {
      if (this.location) {
        this.location = null;
        this.abort = null;
        this.dispatch(this.opts, this);
      } else {
        this.handler.onComplete(trailers);
      }
    }
    onBodySent(chunk) {
      if (this.handler.onBodySent) {
        this.handler.onBodySent(chunk);
      }
    }
  }
  function parseLocation(statusCode, headers) {
    if (redirectableStatusCodes.indexOf(statusCode) === -1) {
      return null;
    }
    for (let i = 0;i < headers.length; i += 2) {
      if (headers[i].length === 8 && util.headerNameToString(headers[i]) === "location") {
        return headers[i + 1];
      }
    }
  }
  function shouldRemoveHeader(header, removeContent, unknownOrigin) {
    if (header.length === 4) {
      return util.headerNameToString(header) === "host";
    }
    if (removeContent && util.headerNameToString(header).startsWith("content-")) {
      return true;
    }
    if (unknownOrigin && (header.length === 13 || header.length === 6 || header.length === 19)) {
      const name = util.headerNameToString(header);
      return name === "authorization" || name === "cookie" || name === "proxy-authorization";
    }
    return false;
  }
  function cleanRequestHeaders(headers, removeContent, unknownOrigin) {
    const ret = [];
    if (Array.isArray(headers)) {
      for (let i = 0;i < headers.length; i += 2) {
        if (!shouldRemoveHeader(headers[i], removeContent, unknownOrigin)) {
          ret.push(headers[i], headers[i + 1]);
        }
      }
    } else if (headers && typeof headers === "object") {
      for (const key of Object.keys(headers)) {
        if (!shouldRemoveHeader(key, removeContent, unknownOrigin)) {
          ret.push(key, headers[key]);
        }
      }
    } else {
      assert(headers == null, "headers must be an object or an array");
    }
    return ret;
  }
  module.exports = RedirectHandler;
});

// node_modules/undici/lib/interceptor/redirect-interceptor.js
var require_redirect_interceptor = __commonJS((exports, module) => {
  var RedirectHandler = require_redirect_handler();
  function createRedirectInterceptor({ maxRedirections: defaultMaxRedirections }) {
    return (dispatch) => {
      return function Intercept(opts, handler) {
        const { maxRedirections = defaultMaxRedirections } = opts;
        if (!maxRedirections) {
          return dispatch(opts, handler);
        }
        const redirectHandler = new RedirectHandler(dispatch, maxRedirections, opts, handler);
        opts = { ...opts, maxRedirections: 0 };
        return dispatch(opts, redirectHandler);
      };
    };
  }
  module.exports = createRedirectInterceptor;
});

// node_modules/undici/lib/dispatcher/client.js
var require_client = __commonJS((exports, module) => {
  var assert = __require("assert");
  var net = __require("net");
  var http = __require("http");
  var util = require_util();
  var { channels } = require_diagnostics();
  var Request2 = require_request();
  var DispatcherBase = require_dispatcher_base();
  var {
    InvalidArgumentError,
    InformationalError,
    ClientDestroyedError
  } = require_errors();
  var buildConnector = require_connect();
  var {
    kUrl,
    kServerName,
    kClient,
    kBusy,
    kConnect,
    kResuming,
    kRunning,
    kPending,
    kSize,
    kQueue,
    kConnected,
    kConnecting,
    kNeedDrain,
    kKeepAliveDefaultTimeout,
    kHostHeader,
    kPendingIdx,
    kRunningIdx,
    kError,
    kPipelining,
    kKeepAliveTimeoutValue,
    kMaxHeadersSize,
    kKeepAliveMaxTimeout,
    kKeepAliveTimeoutThreshold,
    kHeadersTimeout,
    kBodyTimeout,
    kStrictContentLength,
    kConnector,
    kMaxRedirections,
    kMaxRequests,
    kCounter,
    kClose,
    kDestroy,
    kDispatch,
    kInterceptors,
    kLocalAddress,
    kMaxResponseSize,
    kOnError,
    kHTTPContext,
    kMaxConcurrentStreams,
    kResume
  } = require_symbols();
  var connectH1 = require_client_h1();
  var connectH2 = require_client_h2();
  var deprecatedInterceptorWarned = false;
  var kClosedResolve = Symbol("kClosedResolve");
  var noop = () => {};
  function getPipelining(client) {
    return client[kPipelining] ?? client[kHTTPContext]?.defaultPipelining ?? 1;
  }

  class Client extends DispatcherBase {
    constructor(url, {
      interceptors,
      maxHeaderSize,
      headersTimeout,
      socketTimeout,
      requestTimeout,
      connectTimeout,
      bodyTimeout,
      idleTimeout,
      keepAlive,
      keepAliveTimeout,
      maxKeepAliveTimeout,
      keepAliveMaxTimeout,
      keepAliveTimeoutThreshold,
      socketPath,
      pipelining,
      tls,
      strictContentLength,
      maxCachedSessions,
      maxRedirections,
      connect: connect2,
      maxRequestsPerClient,
      localAddress,
      maxResponseSize,
      autoSelectFamily,
      autoSelectFamilyAttemptTimeout,
      maxConcurrentStreams,
      allowH2
    } = {}) {
      super();
      if (keepAlive !== undefined) {
        throw new InvalidArgumentError("unsupported keepAlive, use pipelining=0 instead");
      }
      if (socketTimeout !== undefined) {
        throw new InvalidArgumentError("unsupported socketTimeout, use headersTimeout & bodyTimeout instead");
      }
      if (requestTimeout !== undefined) {
        throw new InvalidArgumentError("unsupported requestTimeout, use headersTimeout & bodyTimeout instead");
      }
      if (idleTimeout !== undefined) {
        throw new InvalidArgumentError("unsupported idleTimeout, use keepAliveTimeout instead");
      }
      if (maxKeepAliveTimeout !== undefined) {
        throw new InvalidArgumentError("unsupported maxKeepAliveTimeout, use keepAliveMaxTimeout instead");
      }
      if (maxHeaderSize != null && !Number.isFinite(maxHeaderSize)) {
        throw new InvalidArgumentError("invalid maxHeaderSize");
      }
      if (socketPath != null && typeof socketPath !== "string") {
        throw new InvalidArgumentError("invalid socketPath");
      }
      if (connectTimeout != null && (!Number.isFinite(connectTimeout) || connectTimeout < 0)) {
        throw new InvalidArgumentError("invalid connectTimeout");
      }
      if (keepAliveTimeout != null && (!Number.isFinite(keepAliveTimeout) || keepAliveTimeout <= 0)) {
        throw new InvalidArgumentError("invalid keepAliveTimeout");
      }
      if (keepAliveMaxTimeout != null && (!Number.isFinite(keepAliveMaxTimeout) || keepAliveMaxTimeout <= 0)) {
        throw new InvalidArgumentError("invalid keepAliveMaxTimeout");
      }
      if (keepAliveTimeoutThreshold != null && !Number.isFinite(keepAliveTimeoutThreshold)) {
        throw new InvalidArgumentError("invalid keepAliveTimeoutThreshold");
      }
      if (headersTimeout != null && (!Number.isInteger(headersTimeout) || headersTimeout < 0)) {
        throw new InvalidArgumentError("headersTimeout must be a positive integer or zero");
      }
      if (bodyTimeout != null && (!Number.isInteger(bodyTimeout) || bodyTimeout < 0)) {
        throw new InvalidArgumentError("bodyTimeout must be a positive integer or zero");
      }
      if (connect2 != null && typeof connect2 !== "function" && typeof connect2 !== "object") {
        throw new InvalidArgumentError("connect must be a function or an object");
      }
      if (maxRedirections != null && (!Number.isInteger(maxRedirections) || maxRedirections < 0)) {
        throw new InvalidArgumentError("maxRedirections must be a positive number");
      }
      if (maxRequestsPerClient != null && (!Number.isInteger(maxRequestsPerClient) || maxRequestsPerClient < 0)) {
        throw new InvalidArgumentError("maxRequestsPerClient must be a positive number");
      }
      if (localAddress != null && (typeof localAddress !== "string" || net.isIP(localAddress) === 0)) {
        throw new InvalidArgumentError("localAddress must be valid string IP address");
      }
      if (maxResponseSize != null && (!Number.isInteger(maxResponseSize) || maxResponseSize < -1)) {
        throw new InvalidArgumentError("maxResponseSize must be a positive number");
      }
      if (autoSelectFamilyAttemptTimeout != null && (!Number.isInteger(autoSelectFamilyAttemptTimeout) || autoSelectFamilyAttemptTimeout < -1)) {
        throw new InvalidArgumentError("autoSelectFamilyAttemptTimeout must be a positive number");
      }
      if (allowH2 != null && typeof allowH2 !== "boolean") {
        throw new InvalidArgumentError("allowH2 must be a valid boolean value");
      }
      if (maxConcurrentStreams != null && (typeof maxConcurrentStreams !== "number" || maxConcurrentStreams < 1)) {
        throw new InvalidArgumentError("maxConcurrentStreams must be a positive integer, greater than 0");
      }
      if (typeof connect2 !== "function") {
        connect2 = buildConnector({
          ...tls,
          maxCachedSessions,
          allowH2,
          socketPath,
          timeout: connectTimeout,
          ...autoSelectFamily ? { autoSelectFamily, autoSelectFamilyAttemptTimeout } : undefined,
          ...connect2
        });
      }
      if (interceptors?.Client && Array.isArray(interceptors.Client)) {
        this[kInterceptors] = interceptors.Client;
        if (!deprecatedInterceptorWarned) {
          deprecatedInterceptorWarned = true;
          process.emitWarning("Client.Options#interceptor is deprecated. Use Dispatcher#compose instead.", {
            code: "UNDICI-CLIENT-INTERCEPTOR-DEPRECATED"
          });
        }
      } else {
        this[kInterceptors] = [createRedirectInterceptor({ maxRedirections })];
      }
      this[kUrl] = util.parseOrigin(url);
      this[kConnector] = connect2;
      this[kPipelining] = pipelining != null ? pipelining : 1;
      this[kMaxHeadersSize] = maxHeaderSize || http.maxHeaderSize;
      this[kKeepAliveDefaultTimeout] = keepAliveTimeout == null ? 4000 : keepAliveTimeout;
      this[kKeepAliveMaxTimeout] = keepAliveMaxTimeout == null ? 600000 : keepAliveMaxTimeout;
      this[kKeepAliveTimeoutThreshold] = keepAliveTimeoutThreshold == null ? 2000 : keepAliveTimeoutThreshold;
      this[kKeepAliveTimeoutValue] = this[kKeepAliveDefaultTimeout];
      this[kServerName] = null;
      this[kLocalAddress] = localAddress != null ? localAddress : null;
      this[kResuming] = 0;
      this[kNeedDrain] = 0;
      this[kHostHeader] = `host: ${this[kUrl].hostname}${this[kUrl].port ? `:${this[kUrl].port}` : ""}\r
`;
      this[kBodyTimeout] = bodyTimeout != null ? bodyTimeout : 300000;
      this[kHeadersTimeout] = headersTimeout != null ? headersTimeout : 300000;
      this[kStrictContentLength] = strictContentLength == null ? true : strictContentLength;
      this[kMaxRedirections] = maxRedirections;
      this[kMaxRequests] = maxRequestsPerClient;
      this[kClosedResolve] = null;
      this[kMaxResponseSize] = maxResponseSize > -1 ? maxResponseSize : -1;
      this[kMaxConcurrentStreams] = maxConcurrentStreams != null ? maxConcurrentStreams : 100;
      this[kHTTPContext] = null;
      this[kQueue] = [];
      this[kRunningIdx] = 0;
      this[kPendingIdx] = 0;
      this[kResume] = (sync) => resume(this, sync);
      this[kOnError] = (err) => onError(this, err);
    }
    get pipelining() {
      return this[kPipelining];
    }
    set pipelining(value) {
      this[kPipelining] = value;
      this[kResume](true);
    }
    get [kPending]() {
      return this[kQueue].length - this[kPendingIdx];
    }
    get [kRunning]() {
      return this[kPendingIdx] - this[kRunningIdx];
    }
    get [kSize]() {
      return this[kQueue].length - this[kRunningIdx];
    }
    get [kConnected]() {
      return !!this[kHTTPContext] && !this[kConnecting] && !this[kHTTPContext].destroyed;
    }
    get [kBusy]() {
      return Boolean(this[kHTTPContext]?.busy(null) || this[kSize] >= (getPipelining(this) || 1) || this[kPending] > 0);
    }
    [kConnect](cb) {
      connect(this);
      this.once("connect", cb);
    }
    [kDispatch](opts, handler) {
      const origin = opts.origin || this[kUrl].origin;
      const request = new Request2(origin, opts, handler);
      this[kQueue].push(request);
      if (this[kResuming]) {} else if (util.bodyLength(request.body) == null && util.isIterable(request.body)) {
        this[kResuming] = 1;
        queueMicrotask(() => resume(this));
      } else {
        this[kResume](true);
      }
      if (this[kResuming] && this[kNeedDrain] !== 2 && this[kBusy]) {
        this[kNeedDrain] = 2;
      }
      return this[kNeedDrain] < 2;
    }
    async[kClose]() {
      return new Promise((resolve3) => {
        if (this[kSize]) {
          this[kClosedResolve] = resolve3;
        } else {
          resolve3(null);
        }
      });
    }
    async[kDestroy](err) {
      return new Promise((resolve3) => {
        const requests = this[kQueue].splice(this[kPendingIdx]);
        for (let i = 0;i < requests.length; i++) {
          const request = requests[i];
          util.errorRequest(this, request, err);
        }
        const callback = () => {
          if (this[kClosedResolve]) {
            this[kClosedResolve]();
            this[kClosedResolve] = null;
          }
          resolve3(null);
        };
        if (this[kHTTPContext]) {
          this[kHTTPContext].destroy(err, callback);
          this[kHTTPContext] = null;
        } else {
          queueMicrotask(callback);
        }
        this[kResume]();
      });
    }
  }
  var createRedirectInterceptor = require_redirect_interceptor();
  function onError(client, err) {
    if (client[kRunning] === 0 && err.code !== "UND_ERR_INFO" && err.code !== "UND_ERR_SOCKET") {
      assert(client[kPendingIdx] === client[kRunningIdx]);
      const requests = client[kQueue].splice(client[kRunningIdx]);
      for (let i = 0;i < requests.length; i++) {
        const request = requests[i];
        util.errorRequest(client, request, err);
      }
      assert(client[kSize] === 0);
    }
  }
  async function connect(client) {
    assert(!client[kConnecting]);
    assert(!client[kHTTPContext]);
    let { host, hostname, protocol, port } = client[kUrl];
    if (hostname[0] === "[") {
      const idx = hostname.indexOf("]");
      assert(idx !== -1);
      const ip = hostname.substring(1, idx);
      assert(net.isIP(ip));
      hostname = ip;
    }
    client[kConnecting] = true;
    if (channels.beforeConnect.hasSubscribers) {
      channels.beforeConnect.publish({
        connectParams: {
          host,
          hostname,
          protocol,
          port,
          version: client[kHTTPContext]?.version,
          servername: client[kServerName],
          localAddress: client[kLocalAddress]
        },
        connector: client[kConnector]
      });
    }
    try {
      const socket = await new Promise((resolve3, reject) => {
        client[kConnector]({
          host,
          hostname,
          protocol,
          port,
          servername: client[kServerName],
          localAddress: client[kLocalAddress]
        }, (err, socket2) => {
          if (err) {
            reject(err);
          } else {
            resolve3(socket2);
          }
        });
      });
      if (client.destroyed) {
        util.destroy(socket.on("error", noop), new ClientDestroyedError);
        return;
      }
      assert(socket);
      try {
        client[kHTTPContext] = socket.alpnProtocol === "h2" ? await connectH2(client, socket) : await connectH1(client, socket);
      } catch (err) {
        socket.destroy().on("error", noop);
        throw err;
      }
      client[kConnecting] = false;
      socket[kCounter] = 0;
      socket[kMaxRequests] = client[kMaxRequests];
      socket[kClient] = client;
      socket[kError] = null;
      if (channels.connected.hasSubscribers) {
        channels.connected.publish({
          connectParams: {
            host,
            hostname,
            protocol,
            port,
            version: client[kHTTPContext]?.version,
            servername: client[kServerName],
            localAddress: client[kLocalAddress]
          },
          connector: client[kConnector],
          socket
        });
      }
      client.emit("connect", client[kUrl], [client]);
    } catch (err) {
      if (client.destroyed) {
        return;
      }
      client[kConnecting] = false;
      if (channels.connectError.hasSubscribers) {
        channels.connectError.publish({
          connectParams: {
            host,
            hostname,
            protocol,
            port,
            version: client[kHTTPContext]?.version,
            servername: client[kServerName],
            localAddress: client[kLocalAddress]
          },
          connector: client[kConnector],
          error: err
        });
      }
      if (err.code === "ERR_TLS_CERT_ALTNAME_INVALID") {
        assert(client[kRunning] === 0);
        while (client[kPending] > 0 && client[kQueue][client[kPendingIdx]].servername === client[kServerName]) {
          const request = client[kQueue][client[kPendingIdx]++];
          util.errorRequest(client, request, err);
        }
      } else {
        onError(client, err);
      }
      client.emit("connectionError", client[kUrl], [client], err);
    }
    client[kResume]();
  }
  function emitDrain(client) {
    client[kNeedDrain] = 0;
    client.emit("drain", client[kUrl], [client]);
  }
  function resume(client, sync) {
    if (client[kResuming] === 2) {
      return;
    }
    client[kResuming] = 2;
    _resume(client, sync);
    client[kResuming] = 0;
    if (client[kRunningIdx] > 256) {
      client[kQueue].splice(0, client[kRunningIdx]);
      client[kPendingIdx] -= client[kRunningIdx];
      client[kRunningIdx] = 0;
    }
  }
  function _resume(client, sync) {
    while (true) {
      if (client.destroyed) {
        assert(client[kPending] === 0);
        return;
      }
      if (client[kClosedResolve] && !client[kSize]) {
        client[kClosedResolve]();
        client[kClosedResolve] = null;
        return;
      }
      if (client[kHTTPContext]) {
        client[kHTTPContext].resume();
      }
      if (client[kBusy]) {
        client[kNeedDrain] = 2;
      } else if (client[kNeedDrain] === 2) {
        if (sync) {
          client[kNeedDrain] = 1;
          queueMicrotask(() => emitDrain(client));
        } else {
          emitDrain(client);
        }
        continue;
      }
      if (client[kPending] === 0) {
        return;
      }
      if (client[kRunning] >= (getPipelining(client) || 1)) {
        return;
      }
      const request = client[kQueue][client[kPendingIdx]];
      if (client[kUrl].protocol === "https:" && client[kServerName] !== request.servername) {
        if (client[kRunning] > 0) {
          return;
        }
        client[kServerName] = request.servername;
        client[kHTTPContext]?.destroy(new InformationalError("servername changed"), () => {
          client[kHTTPContext] = null;
          resume(client);
        });
      }
      if (client[kConnecting]) {
        return;
      }
      if (!client[kHTTPContext]) {
        connect(client);
        return;
      }
      if (client[kHTTPContext].destroyed) {
        return;
      }
      if (client[kHTTPContext].busy(request)) {
        return;
      }
      if (!request.aborted && client[kHTTPContext].write(request)) {
        client[kPendingIdx]++;
      } else {
        client[kQueue].splice(client[kPendingIdx], 1);
      }
    }
  }
  module.exports = Client;
});

// node_modules/undici/lib/dispatcher/fixed-queue.js
var require_fixed_queue = __commonJS((exports, module) => {
  var kSize = 2048;
  var kMask = kSize - 1;

  class FixedCircularBuffer {
    constructor() {
      this.bottom = 0;
      this.top = 0;
      this.list = new Array(kSize);
      this.next = null;
    }
    isEmpty() {
      return this.top === this.bottom;
    }
    isFull() {
      return (this.top + 1 & kMask) === this.bottom;
    }
    push(data) {
      this.list[this.top] = data;
      this.top = this.top + 1 & kMask;
    }
    shift() {
      const nextItem = this.list[this.bottom];
      if (nextItem === undefined)
        return null;
      this.list[this.bottom] = undefined;
      this.bottom = this.bottom + 1 & kMask;
      return nextItem;
    }
  }
  module.exports = class FixedQueue {
    constructor() {
      this.head = this.tail = new FixedCircularBuffer;
    }
    isEmpty() {
      return this.head.isEmpty();
    }
    push(data) {
      if (this.head.isFull()) {
        this.head = this.head.next = new FixedCircularBuffer;
      }
      this.head.push(data);
    }
    shift() {
      const tail = this.tail;
      const next = tail.shift();
      if (tail.isEmpty() && tail.next !== null) {
        this.tail = tail.next;
      }
      return next;
    }
  };
});

// node_modules/undici/lib/dispatcher/pool-stats.js
var require_pool_stats = __commonJS((exports, module) => {
  var { kFree, kConnected, kPending, kQueued, kRunning, kSize } = require_symbols();
  var kPool = Symbol("pool");

  class PoolStats {
    constructor(pool2) {
      this[kPool] = pool2;
    }
    get connected() {
      return this[kPool][kConnected];
    }
    get free() {
      return this[kPool][kFree];
    }
    get pending() {
      return this[kPool][kPending];
    }
    get queued() {
      return this[kPool][kQueued];
    }
    get running() {
      return this[kPool][kRunning];
    }
    get size() {
      return this[kPool][kSize];
    }
  }
  module.exports = PoolStats;
});

// node_modules/undici/lib/dispatcher/pool-base.js
var require_pool_base = __commonJS((exports, module) => {
  var DispatcherBase = require_dispatcher_base();
  var FixedQueue = require_fixed_queue();
  var { kConnected, kSize, kRunning, kPending, kQueued, kBusy, kFree, kUrl, kClose, kDestroy, kDispatch } = require_symbols();
  var PoolStats = require_pool_stats();
  var kClients = Symbol("clients");
  var kNeedDrain = Symbol("needDrain");
  var kQueue = Symbol("queue");
  var kClosedResolve = Symbol("closed resolve");
  var kOnDrain = Symbol("onDrain");
  var kOnConnect = Symbol("onConnect");
  var kOnDisconnect = Symbol("onDisconnect");
  var kOnConnectionError = Symbol("onConnectionError");
  var kGetDispatcher = Symbol("get dispatcher");
  var kAddClient = Symbol("add client");
  var kRemoveClient = Symbol("remove client");
  var kStats = Symbol("stats");

  class PoolBase extends DispatcherBase {
    constructor() {
      super();
      this[kQueue] = new FixedQueue;
      this[kClients] = [];
      this[kQueued] = 0;
      const pool2 = this;
      this[kOnDrain] = function onDrain(origin, targets) {
        const queue = pool2[kQueue];
        let needDrain = false;
        while (!needDrain) {
          const item = queue.shift();
          if (!item) {
            break;
          }
          pool2[kQueued]--;
          needDrain = !this.dispatch(item.opts, item.handler);
        }
        this[kNeedDrain] = needDrain;
        if (!this[kNeedDrain] && pool2[kNeedDrain]) {
          pool2[kNeedDrain] = false;
          pool2.emit("drain", origin, [pool2, ...targets]);
        }
        if (pool2[kClosedResolve] && queue.isEmpty()) {
          Promise.all(pool2[kClients].map((c) => c.close())).then(pool2[kClosedResolve]);
        }
      };
      this[kOnConnect] = (origin, targets) => {
        pool2.emit("connect", origin, [pool2, ...targets]);
      };
      this[kOnDisconnect] = (origin, targets, err) => {
        pool2.emit("disconnect", origin, [pool2, ...targets], err);
      };
      this[kOnConnectionError] = (origin, targets, err) => {
        pool2.emit("connectionError", origin, [pool2, ...targets], err);
      };
      this[kStats] = new PoolStats(this);
    }
    get [kBusy]() {
      return this[kNeedDrain];
    }
    get [kConnected]() {
      return this[kClients].filter((client) => client[kConnected]).length;
    }
    get [kFree]() {
      return this[kClients].filter((client) => client[kConnected] && !client[kNeedDrain]).length;
    }
    get [kPending]() {
      let ret = this[kQueued];
      for (const { [kPending]: pending } of this[kClients]) {
        ret += pending;
      }
      return ret;
    }
    get [kRunning]() {
      let ret = 0;
      for (const { [kRunning]: running } of this[kClients]) {
        ret += running;
      }
      return ret;
    }
    get [kSize]() {
      let ret = this[kQueued];
      for (const { [kSize]: size } of this[kClients]) {
        ret += size;
      }
      return ret;
    }
    get stats() {
      return this[kStats];
    }
    async[kClose]() {
      if (this[kQueue].isEmpty()) {
        await Promise.all(this[kClients].map((c) => c.close()));
      } else {
        await new Promise((resolve3) => {
          this[kClosedResolve] = resolve3;
        });
      }
    }
    async[kDestroy](err) {
      while (true) {
        const item = this[kQueue].shift();
        if (!item) {
          break;
        }
        item.handler.onError(err);
      }
      await Promise.all(this[kClients].map((c) => c.destroy(err)));
    }
    [kDispatch](opts, handler) {
      const dispatcher = this[kGetDispatcher]();
      if (!dispatcher) {
        this[kNeedDrain] = true;
        this[kQueue].push({ opts, handler });
        this[kQueued]++;
      } else if (!dispatcher.dispatch(opts, handler)) {
        dispatcher[kNeedDrain] = true;
        this[kNeedDrain] = !this[kGetDispatcher]();
      }
      return !this[kNeedDrain];
    }
    [kAddClient](client) {
      client.on("drain", this[kOnDrain]).on("connect", this[kOnConnect]).on("disconnect", this[kOnDisconnect]).on("connectionError", this[kOnConnectionError]);
      this[kClients].push(client);
      if (this[kNeedDrain]) {
        queueMicrotask(() => {
          if (this[kNeedDrain]) {
            this[kOnDrain](client[kUrl], [this, client]);
          }
        });
      }
      return this;
    }
    [kRemoveClient](client) {
      client.close(() => {
        const idx = this[kClients].indexOf(client);
        if (idx !== -1) {
          this[kClients].splice(idx, 1);
        }
      });
      this[kNeedDrain] = this[kClients].some((dispatcher) => !dispatcher[kNeedDrain] && dispatcher.closed !== true && dispatcher.destroyed !== true);
    }
  }
  module.exports = {
    PoolBase,
    kClients,
    kNeedDrain,
    kAddClient,
    kRemoveClient,
    kGetDispatcher
  };
});

// node_modules/undici/lib/dispatcher/pool.js
var require_pool = __commonJS((exports, module) => {
  var {
    PoolBase,
    kClients,
    kNeedDrain,
    kAddClient,
    kGetDispatcher
  } = require_pool_base();
  var Client = require_client();
  var {
    InvalidArgumentError
  } = require_errors();
  var util = require_util();
  var { kUrl, kInterceptors } = require_symbols();
  var buildConnector = require_connect();
  var kOptions = Symbol("options");
  var kConnections = Symbol("connections");
  var kFactory = Symbol("factory");
  function defaultFactory(origin, opts) {
    return new Client(origin, opts);
  }

  class Pool extends PoolBase {
    constructor(origin, {
      connections,
      factory = defaultFactory,
      connect,
      connectTimeout,
      tls,
      maxCachedSessions,
      socketPath,
      autoSelectFamily,
      autoSelectFamilyAttemptTimeout,
      allowH2,
      ...options
    } = {}) {
      super();
      if (connections != null && (!Number.isFinite(connections) || connections < 0)) {
        throw new InvalidArgumentError("invalid connections");
      }
      if (typeof factory !== "function") {
        throw new InvalidArgumentError("factory must be a function.");
      }
      if (connect != null && typeof connect !== "function" && typeof connect !== "object") {
        throw new InvalidArgumentError("connect must be a function or an object");
      }
      if (typeof connect !== "function") {
        connect = buildConnector({
          ...tls,
          maxCachedSessions,
          allowH2,
          socketPath,
          timeout: connectTimeout,
          ...autoSelectFamily ? { autoSelectFamily, autoSelectFamilyAttemptTimeout } : undefined,
          ...connect
        });
      }
      this[kInterceptors] = options.interceptors?.Pool && Array.isArray(options.interceptors.Pool) ? options.interceptors.Pool : [];
      this[kConnections] = connections || null;
      this[kUrl] = util.parseOrigin(origin);
      this[kOptions] = { ...util.deepClone(options), connect, allowH2 };
      this[kOptions].interceptors = options.interceptors ? { ...options.interceptors } : undefined;
      this[kFactory] = factory;
      this.on("connectionError", (origin2, targets, error) => {
        for (const target of targets) {
          const idx = this[kClients].indexOf(target);
          if (idx !== -1) {
            this[kClients].splice(idx, 1);
          }
        }
      });
    }
    [kGetDispatcher]() {
      for (const client of this[kClients]) {
        if (!client[kNeedDrain]) {
          return client;
        }
      }
      if (!this[kConnections] || this[kClients].length < this[kConnections]) {
        const dispatcher = this[kFactory](this[kUrl], this[kOptions]);
        this[kAddClient](dispatcher);
        return dispatcher;
      }
    }
  }
  module.exports = Pool;
});

// node_modules/undici/lib/dispatcher/balanced-pool.js
var require_balanced_pool = __commonJS((exports, module) => {
  var {
    BalancedPoolMissingUpstreamError,
    InvalidArgumentError
  } = require_errors();
  var {
    PoolBase,
    kClients,
    kNeedDrain,
    kAddClient,
    kRemoveClient,
    kGetDispatcher
  } = require_pool_base();
  var Pool = require_pool();
  var { kUrl, kInterceptors } = require_symbols();
  var { parseOrigin } = require_util();
  var kFactory = Symbol("factory");
  var kOptions = Symbol("options");
  var kGreatestCommonDivisor = Symbol("kGreatestCommonDivisor");
  var kCurrentWeight = Symbol("kCurrentWeight");
  var kIndex = Symbol("kIndex");
  var kWeight = Symbol("kWeight");
  var kMaxWeightPerServer = Symbol("kMaxWeightPerServer");
  var kErrorPenalty = Symbol("kErrorPenalty");
  function getGreatestCommonDivisor(a, b) {
    if (a === 0)
      return b;
    while (b !== 0) {
      const t = b;
      b = a % b;
      a = t;
    }
    return a;
  }
  function defaultFactory(origin, opts) {
    return new Pool(origin, opts);
  }

  class BalancedPool extends PoolBase {
    constructor(upstreams = [], { factory = defaultFactory, ...opts } = {}) {
      super();
      this[kOptions] = opts;
      this[kIndex] = -1;
      this[kCurrentWeight] = 0;
      this[kMaxWeightPerServer] = this[kOptions].maxWeightPerServer || 100;
      this[kErrorPenalty] = this[kOptions].errorPenalty || 15;
      if (!Array.isArray(upstreams)) {
        upstreams = [upstreams];
      }
      if (typeof factory !== "function") {
        throw new InvalidArgumentError("factory must be a function.");
      }
      this[kInterceptors] = opts.interceptors?.BalancedPool && Array.isArray(opts.interceptors.BalancedPool) ? opts.interceptors.BalancedPool : [];
      this[kFactory] = factory;
      for (const upstream of upstreams) {
        this.addUpstream(upstream);
      }
      this._updateBalancedPoolStats();
    }
    addUpstream(upstream) {
      const upstreamOrigin = parseOrigin(upstream).origin;
      if (this[kClients].find((pool3) => pool3[kUrl].origin === upstreamOrigin && pool3.closed !== true && pool3.destroyed !== true)) {
        return this;
      }
      const pool2 = this[kFactory](upstreamOrigin, Object.assign({}, this[kOptions]));
      this[kAddClient](pool2);
      pool2.on("connect", () => {
        pool2[kWeight] = Math.min(this[kMaxWeightPerServer], pool2[kWeight] + this[kErrorPenalty]);
      });
      pool2.on("connectionError", () => {
        pool2[kWeight] = Math.max(1, pool2[kWeight] - this[kErrorPenalty]);
        this._updateBalancedPoolStats();
      });
      pool2.on("disconnect", (...args) => {
        const err = args[2];
        if (err && err.code === "UND_ERR_SOCKET") {
          pool2[kWeight] = Math.max(1, pool2[kWeight] - this[kErrorPenalty]);
          this._updateBalancedPoolStats();
        }
      });
      for (const client of this[kClients]) {
        client[kWeight] = this[kMaxWeightPerServer];
      }
      this._updateBalancedPoolStats();
      return this;
    }
    _updateBalancedPoolStats() {
      let result = 0;
      for (let i = 0;i < this[kClients].length; i++) {
        result = getGreatestCommonDivisor(this[kClients][i][kWeight], result);
      }
      this[kGreatestCommonDivisor] = result;
    }
    removeUpstream(upstream) {
      const upstreamOrigin = parseOrigin(upstream).origin;
      const pool2 = this[kClients].find((pool3) => pool3[kUrl].origin === upstreamOrigin && pool3.closed !== true && pool3.destroyed !== true);
      if (pool2) {
        this[kRemoveClient](pool2);
      }
      return this;
    }
    get upstreams() {
      return this[kClients].filter((dispatcher) => dispatcher.closed !== true && dispatcher.destroyed !== true).map((p) => p[kUrl].origin);
    }
    [kGetDispatcher]() {
      if (this[kClients].length === 0) {
        throw new BalancedPoolMissingUpstreamError;
      }
      const dispatcher = this[kClients].find((dispatcher2) => !dispatcher2[kNeedDrain] && dispatcher2.closed !== true && dispatcher2.destroyed !== true);
      if (!dispatcher) {
        return;
      }
      const allClientsBusy = this[kClients].map((pool2) => pool2[kNeedDrain]).reduce((a, b) => a && b, true);
      if (allClientsBusy) {
        return;
      }
      let counter = 0;
      let maxWeightIndex = this[kClients].findIndex((pool2) => !pool2[kNeedDrain]);
      while (counter++ < this[kClients].length) {
        this[kIndex] = (this[kIndex] + 1) % this[kClients].length;
        const pool2 = this[kClients][this[kIndex]];
        if (pool2[kWeight] > this[kClients][maxWeightIndex][kWeight] && !pool2[kNeedDrain]) {
          maxWeightIndex = this[kIndex];
        }
        if (this[kIndex] === 0) {
          this[kCurrentWeight] = this[kCurrentWeight] - this[kGreatestCommonDivisor];
          if (this[kCurrentWeight] <= 0) {
            this[kCurrentWeight] = this[kMaxWeightPerServer];
          }
        }
        if (pool2[kWeight] >= this[kCurrentWeight] && !pool2[kNeedDrain]) {
          return pool2;
        }
      }
      this[kCurrentWeight] = this[kClients][maxWeightIndex][kWeight];
      this[kIndex] = maxWeightIndex;
      return this[kClients][maxWeightIndex];
    }
  }
  module.exports = BalancedPool;
});

// node_modules/undici/lib/dispatcher/agent.js
var require_agent = __commonJS((exports, module) => {
  var { InvalidArgumentError } = require_errors();
  var { kClients, kRunning, kClose, kDestroy, kDispatch, kInterceptors } = require_symbols();
  var DispatcherBase = require_dispatcher_base();
  var Pool = require_pool();
  var Client = require_client();
  var util = require_util();
  var createRedirectInterceptor = require_redirect_interceptor();
  var kOnConnect = Symbol("onConnect");
  var kOnDisconnect = Symbol("onDisconnect");
  var kOnConnectionError = Symbol("onConnectionError");
  var kMaxRedirections = Symbol("maxRedirections");
  var kOnDrain = Symbol("onDrain");
  var kFactory = Symbol("factory");
  var kOptions = Symbol("options");
  function defaultFactory(origin, opts) {
    return opts && opts.connections === 1 ? new Client(origin, opts) : new Pool(origin, opts);
  }

  class Agent extends DispatcherBase {
    constructor({ factory = defaultFactory, maxRedirections = 0, connect, ...options } = {}) {
      super();
      if (typeof factory !== "function") {
        throw new InvalidArgumentError("factory must be a function.");
      }
      if (connect != null && typeof connect !== "function" && typeof connect !== "object") {
        throw new InvalidArgumentError("connect must be a function or an object");
      }
      if (!Number.isInteger(maxRedirections) || maxRedirections < 0) {
        throw new InvalidArgumentError("maxRedirections must be a positive number");
      }
      if (connect && typeof connect !== "function") {
        connect = { ...connect };
      }
      this[kInterceptors] = options.interceptors?.Agent && Array.isArray(options.interceptors.Agent) ? options.interceptors.Agent : [createRedirectInterceptor({ maxRedirections })];
      this[kOptions] = { ...util.deepClone(options), connect };
      this[kOptions].interceptors = options.interceptors ? { ...options.interceptors } : undefined;
      this[kMaxRedirections] = maxRedirections;
      this[kFactory] = factory;
      this[kClients] = new Map;
      this[kOnDrain] = (origin, targets) => {
        this.emit("drain", origin, [this, ...targets]);
      };
      this[kOnConnect] = (origin, targets) => {
        this.emit("connect", origin, [this, ...targets]);
      };
      this[kOnDisconnect] = (origin, targets, err) => {
        this.emit("disconnect", origin, [this, ...targets], err);
      };
      this[kOnConnectionError] = (origin, targets, err) => {
        this.emit("connectionError", origin, [this, ...targets], err);
      };
    }
    get [kRunning]() {
      let ret = 0;
      for (const client of this[kClients].values()) {
        ret += client[kRunning];
      }
      return ret;
    }
    [kDispatch](opts, handler) {
      let key;
      if (opts.origin && (typeof opts.origin === "string" || opts.origin instanceof URL)) {
        key = String(opts.origin);
      } else {
        throw new InvalidArgumentError("opts.origin must be a non-empty string or URL.");
      }
      let dispatcher = this[kClients].get(key);
      if (!dispatcher) {
        dispatcher = this[kFactory](opts.origin, this[kOptions]).on("drain", this[kOnDrain]).on("connect", this[kOnConnect]).on("disconnect", this[kOnDisconnect]).on("connectionError", this[kOnConnectionError]);
        this[kClients].set(key, dispatcher);
      }
      return dispatcher.dispatch(opts, handler);
    }
    async[kClose]() {
      const closePromises = [];
      for (const client of this[kClients].values()) {
        closePromises.push(client.close());
      }
      this[kClients].clear();
      await Promise.all(closePromises);
    }
    async[kDestroy](err) {
      const destroyPromises = [];
      for (const client of this[kClients].values()) {
        destroyPromises.push(client.destroy(err));
      }
      this[kClients].clear();
      await Promise.all(destroyPromises);
    }
  }
  module.exports = Agent;
});

// node_modules/undici/lib/dispatcher/proxy-agent.js
var require_proxy_agent = __commonJS((exports, module) => {
  var { kProxy, kClose, kDestroy, kDispatch, kInterceptors } = require_symbols();
  var { URL: URL2 } = __require("url");
  var Agent = require_agent();
  var Pool = require_pool();
  var DispatcherBase = require_dispatcher_base();
  var { InvalidArgumentError, RequestAbortedError, SecureProxyConnectionError } = require_errors();
  var buildConnector = require_connect();
  var Client = require_client();
  var kAgent = Symbol("proxy agent");
  var kClient = Symbol("proxy client");
  var kProxyHeaders = Symbol("proxy headers");
  var kRequestTls = Symbol("request tls settings");
  var kProxyTls = Symbol("proxy tls settings");
  var kConnectEndpoint = Symbol("connect endpoint function");
  var kTunnelProxy = Symbol("tunnel proxy");
  function defaultProtocolPort(protocol) {
    return protocol === "https:" ? 443 : 80;
  }
  function defaultFactory(origin, opts) {
    return new Pool(origin, opts);
  }
  var noop = () => {};
  function defaultAgentFactory(origin, opts) {
    if (opts.connections === 1) {
      return new Client(origin, opts);
    }
    return new Pool(origin, opts);
  }

  class Http1ProxyWrapper extends DispatcherBase {
    #client;
    constructor(proxyUrl, { headers = {}, connect, factory }) {
      super();
      if (!proxyUrl) {
        throw new InvalidArgumentError("Proxy URL is mandatory");
      }
      this[kProxyHeaders] = headers;
      if (factory) {
        this.#client = factory(proxyUrl, { connect });
      } else {
        this.#client = new Client(proxyUrl, { connect });
      }
    }
    [kDispatch](opts, handler) {
      const onHeaders = handler.onHeaders;
      handler.onHeaders = function(statusCode, data, resume) {
        if (statusCode === 407) {
          if (typeof handler.onError === "function") {
            handler.onError(new InvalidArgumentError("Proxy Authentication Required (407)"));
          }
          return;
        }
        if (onHeaders)
          onHeaders.call(this, statusCode, data, resume);
      };
      const {
        origin,
        path = "/",
        headers = {}
      } = opts;
      opts.path = origin + path;
      if (!("host" in headers) && !("Host" in headers)) {
        const { host } = new URL2(origin);
        headers.host = host;
      }
      opts.headers = { ...this[kProxyHeaders], ...headers };
      return this.#client[kDispatch](opts, handler);
    }
    async[kClose]() {
      return this.#client.close();
    }
    async[kDestroy](err) {
      return this.#client.destroy(err);
    }
  }

  class ProxyAgent extends DispatcherBase {
    constructor(opts) {
      super();
      if (!opts || typeof opts === "object" && !(opts instanceof URL2) && !opts.uri) {
        throw new InvalidArgumentError("Proxy uri is mandatory");
      }
      const { clientFactory = defaultFactory } = opts;
      if (typeof clientFactory !== "function") {
        throw new InvalidArgumentError("Proxy opts.clientFactory must be a function.");
      }
      const { proxyTunnel = true } = opts;
      const url = this.#getUrl(opts);
      const { href, origin, port, protocol, username, password, hostname: proxyHostname } = url;
      this[kProxy] = { uri: href, protocol };
      this[kInterceptors] = opts.interceptors?.ProxyAgent && Array.isArray(opts.interceptors.ProxyAgent) ? opts.interceptors.ProxyAgent : [];
      this[kRequestTls] = opts.requestTls;
      this[kProxyTls] = opts.proxyTls;
      this[kProxyHeaders] = opts.headers || {};
      this[kTunnelProxy] = proxyTunnel;
      if (opts.auth && opts.token) {
        throw new InvalidArgumentError("opts.auth cannot be used in combination with opts.token");
      } else if (opts.auth) {
        this[kProxyHeaders]["proxy-authorization"] = `Basic ${opts.auth}`;
      } else if (opts.token) {
        this[kProxyHeaders]["proxy-authorization"] = opts.token;
      } else if (username && password) {
        this[kProxyHeaders]["proxy-authorization"] = `Basic ${Buffer.from(`${decodeURIComponent(username)}:${decodeURIComponent(password)}`).toString("base64")}`;
      }
      const connect = buildConnector({ ...opts.proxyTls });
      this[kConnectEndpoint] = buildConnector({ ...opts.requestTls });
      const agentFactory = opts.factory || defaultAgentFactory;
      const factory = (origin2, options) => {
        const { protocol: protocol2 } = new URL2(origin2);
        if (!this[kTunnelProxy] && protocol2 === "http:" && this[kProxy].protocol === "http:") {
          return new Http1ProxyWrapper(this[kProxy].uri, {
            headers: this[kProxyHeaders],
            connect,
            factory: agentFactory
          });
        }
        return agentFactory(origin2, options);
      };
      this[kClient] = clientFactory(url, { connect });
      this[kAgent] = new Agent({
        ...opts,
        factory,
        connect: async (opts2, callback) => {
          let requestedPath = opts2.host;
          if (!opts2.port) {
            requestedPath += `:${defaultProtocolPort(opts2.protocol)}`;
          }
          try {
            const { socket, statusCode } = await this[kClient].connect({
              origin,
              port,
              path: requestedPath,
              signal: opts2.signal,
              headers: {
                ...this[kProxyHeaders],
                host: opts2.host
              },
              servername: this[kProxyTls]?.servername || proxyHostname
            });
            if (statusCode !== 200) {
              socket.on("error", noop).destroy();
              callback(new RequestAbortedError(`Proxy response (${statusCode}) !== 200 when HTTP Tunneling`));
            }
            if (opts2.protocol !== "https:") {
              callback(null, socket);
              return;
            }
            let servername;
            if (this[kRequestTls]) {
              servername = this[kRequestTls].servername;
            } else {
              servername = opts2.servername;
            }
            this[kConnectEndpoint]({ ...opts2, servername, httpSocket: socket }, callback);
          } catch (err) {
            if (err.code === "ERR_TLS_CERT_ALTNAME_INVALID") {
              callback(new SecureProxyConnectionError(err));
            } else {
              callback(err);
            }
          }
        }
      });
    }
    dispatch(opts, handler) {
      const headers = buildHeaders(opts.headers);
      throwIfProxyAuthIsSent(headers);
      if (headers && !("host" in headers) && !("Host" in headers)) {
        const { host } = new URL2(opts.origin);
        headers.host = host;
      }
      return this[kAgent].dispatch({
        ...opts,
        headers
      }, handler);
    }
    #getUrl(opts) {
      if (typeof opts === "string") {
        return new URL2(opts);
      } else if (opts instanceof URL2) {
        return opts;
      } else {
        return new URL2(opts.uri);
      }
    }
    async[kClose]() {
      await this[kAgent].close();
      await this[kClient].close();
    }
    async[kDestroy]() {
      await this[kAgent].destroy();
      await this[kClient].destroy();
    }
  }
  function buildHeaders(headers) {
    if (Array.isArray(headers)) {
      const headersPair = {};
      for (let i = 0;i < headers.length; i += 2) {
        headersPair[headers[i]] = headers[i + 1];
      }
      return headersPair;
    }
    return headers;
  }
  function throwIfProxyAuthIsSent(headers) {
    const existProxyAuth = headers && Object.keys(headers).find((key) => key.toLowerCase() === "proxy-authorization");
    if (existProxyAuth) {
      throw new InvalidArgumentError("Proxy-Authorization should be sent in ProxyAgent constructor");
    }
  }
  module.exports = ProxyAgent;
});

// node_modules/undici/lib/dispatcher/env-http-proxy-agent.js
var require_env_http_proxy_agent = __commonJS((exports, module) => {
  var DispatcherBase = require_dispatcher_base();
  var { kClose, kDestroy, kClosed, kDestroyed, kDispatch, kNoProxyAgent, kHttpProxyAgent, kHttpsProxyAgent } = require_symbols();
  var ProxyAgent = require_proxy_agent();
  var Agent = require_agent();
  var DEFAULT_PORTS = {
    "http:": 80,
    "https:": 443
  };
  var experimentalWarned = false;

  class EnvHttpProxyAgent extends DispatcherBase {
    #noProxyValue = null;
    #noProxyEntries = null;
    #opts = null;
    constructor(opts = {}) {
      super();
      this.#opts = opts;
      if (!experimentalWarned) {
        experimentalWarned = true;
        process.emitWarning("EnvHttpProxyAgent is experimental, expect them to change at any time.", {
          code: "UNDICI-EHPA"
        });
      }
      const { httpProxy, httpsProxy, noProxy, ...agentOpts } = opts;
      this[kNoProxyAgent] = new Agent(agentOpts);
      const HTTP_PROXY = httpProxy ?? process.env.http_proxy ?? process.env.HTTP_PROXY;
      if (HTTP_PROXY) {
        this[kHttpProxyAgent] = new ProxyAgent({ ...agentOpts, uri: HTTP_PROXY });
      } else {
        this[kHttpProxyAgent] = this[kNoProxyAgent];
      }
      const HTTPS_PROXY = httpsProxy ?? process.env.https_proxy ?? process.env.HTTPS_PROXY;
      if (HTTPS_PROXY) {
        this[kHttpsProxyAgent] = new ProxyAgent({ ...agentOpts, uri: HTTPS_PROXY });
      } else {
        this[kHttpsProxyAgent] = this[kHttpProxyAgent];
      }
      this.#parseNoProxy();
    }
    [kDispatch](opts, handler) {
      const url = new URL(opts.origin);
      const agent = this.#getProxyAgentForUrl(url);
      return agent.dispatch(opts, handler);
    }
    async[kClose]() {
      await this[kNoProxyAgent].close();
      if (!this[kHttpProxyAgent][kClosed]) {
        await this[kHttpProxyAgent].close();
      }
      if (!this[kHttpsProxyAgent][kClosed]) {
        await this[kHttpsProxyAgent].close();
      }
    }
    async[kDestroy](err) {
      await this[kNoProxyAgent].destroy(err);
      if (!this[kHttpProxyAgent][kDestroyed]) {
        await this[kHttpProxyAgent].destroy(err);
      }
      if (!this[kHttpsProxyAgent][kDestroyed]) {
        await this[kHttpsProxyAgent].destroy(err);
      }
    }
    #getProxyAgentForUrl(url) {
      let { protocol, host: hostname, port } = url;
      hostname = hostname.replace(/:\d*$/, "").toLowerCase();
      port = Number.parseInt(port, 10) || DEFAULT_PORTS[protocol] || 0;
      if (!this.#shouldProxy(hostname, port)) {
        return this[kNoProxyAgent];
      }
      if (protocol === "https:") {
        return this[kHttpsProxyAgent];
      }
      return this[kHttpProxyAgent];
    }
    #shouldProxy(hostname, port) {
      if (this.#noProxyChanged) {
        this.#parseNoProxy();
      }
      if (this.#noProxyEntries.length === 0) {
        return true;
      }
      if (this.#noProxyValue === "*") {
        return false;
      }
      for (let i = 0;i < this.#noProxyEntries.length; i++) {
        const entry = this.#noProxyEntries[i];
        if (entry.port && entry.port !== port) {
          continue;
        }
        if (!/^[.*]/.test(entry.hostname)) {
          if (hostname === entry.hostname) {
            return false;
          }
        } else {
          if (hostname.endsWith(entry.hostname.replace(/^\*/, ""))) {
            return false;
          }
        }
      }
      return true;
    }
    #parseNoProxy() {
      const noProxyValue = this.#opts.noProxy ?? this.#noProxyEnv;
      const noProxySplit = noProxyValue.split(/[,\s]/);
      const noProxyEntries = [];
      for (let i = 0;i < noProxySplit.length; i++) {
        const entry = noProxySplit[i];
        if (!entry) {
          continue;
        }
        const parsed = entry.match(/^(.+):(\d+)$/);
        noProxyEntries.push({
          hostname: (parsed ? parsed[1] : entry).toLowerCase(),
          port: parsed ? Number.parseInt(parsed[2], 10) : 0
        });
      }
      this.#noProxyValue = noProxyValue;
      this.#noProxyEntries = noProxyEntries;
    }
    get #noProxyChanged() {
      if (this.#opts.noProxy !== undefined) {
        return false;
      }
      return this.#noProxyValue !== this.#noProxyEnv;
    }
    get #noProxyEnv() {
      return process.env.no_proxy ?? process.env.NO_PROXY ?? "";
    }
  }
  module.exports = EnvHttpProxyAgent;
});

// node_modules/undici/lib/handler/retry-handler.js
var require_retry_handler = __commonJS((exports, module) => {
  var assert = __require("assert");
  var { kRetryHandlerDefaultRetry } = require_symbols();
  var { RequestRetryError } = require_errors();
  var {
    isDisturbed,
    parseHeaders,
    parseRangeHeader,
    wrapRequestBody
  } = require_util();
  function calculateRetryAfterHeader(retryAfter) {
    const current = Date.now();
    return new Date(retryAfter).getTime() - current;
  }

  class RetryHandler {
    constructor(opts, handlers) {
      const { retryOptions, ...dispatchOpts } = opts;
      const {
        retry: retryFn,
        maxRetries,
        maxTimeout,
        minTimeout,
        timeoutFactor,
        methods,
        errorCodes,
        retryAfter,
        statusCodes
      } = retryOptions ?? {};
      this.dispatch = handlers.dispatch;
      this.handler = handlers.handler;
      this.opts = { ...dispatchOpts, body: wrapRequestBody(opts.body) };
      this.abort = null;
      this.aborted = false;
      this.retryOpts = {
        retry: retryFn ?? RetryHandler[kRetryHandlerDefaultRetry],
        retryAfter: retryAfter ?? true,
        maxTimeout: maxTimeout ?? 30 * 1000,
        minTimeout: minTimeout ?? 500,
        timeoutFactor: timeoutFactor ?? 2,
        maxRetries: maxRetries ?? 5,
        methods: methods ?? ["GET", "HEAD", "OPTIONS", "PUT", "DELETE", "TRACE"],
        statusCodes: statusCodes ?? [500, 502, 503, 504, 429],
        errorCodes: errorCodes ?? [
          "ECONNRESET",
          "ECONNREFUSED",
          "ENOTFOUND",
          "ENETDOWN",
          "ENETUNREACH",
          "EHOSTDOWN",
          "EHOSTUNREACH",
          "EPIPE",
          "UND_ERR_SOCKET"
        ]
      };
      this.retryCount = 0;
      this.retryCountCheckpoint = 0;
      this.start = 0;
      this.end = null;
      this.etag = null;
      this.resume = null;
      this.handler.onConnect((reason) => {
        this.aborted = true;
        if (this.abort) {
          this.abort(reason);
        } else {
          this.reason = reason;
        }
      });
    }
    onRequestSent() {
      if (this.handler.onRequestSent) {
        this.handler.onRequestSent();
      }
    }
    onUpgrade(statusCode, headers, socket) {
      if (this.handler.onUpgrade) {
        this.handler.onUpgrade(statusCode, headers, socket);
      }
    }
    onConnect(abort) {
      if (this.aborted) {
        abort(this.reason);
      } else {
        this.abort = abort;
      }
    }
    onBodySent(chunk) {
      if (this.handler.onBodySent)
        return this.handler.onBodySent(chunk);
    }
    static [kRetryHandlerDefaultRetry](err, { state, opts }, cb) {
      const { statusCode, code, headers } = err;
      const { method, retryOptions } = opts;
      const {
        maxRetries,
        minTimeout,
        maxTimeout,
        timeoutFactor,
        statusCodes,
        errorCodes,
        methods
      } = retryOptions;
      const { counter } = state;
      if (code && code !== "UND_ERR_REQ_RETRY" && !errorCodes.includes(code)) {
        cb(err);
        return;
      }
      if (Array.isArray(methods) && !methods.includes(method)) {
        cb(err);
        return;
      }
      if (statusCode != null && Array.isArray(statusCodes) && !statusCodes.includes(statusCode)) {
        cb(err);
        return;
      }
      if (counter > maxRetries) {
        cb(err);
        return;
      }
      let retryAfterHeader = headers?.["retry-after"];
      if (retryAfterHeader) {
        retryAfterHeader = Number(retryAfterHeader);
        retryAfterHeader = Number.isNaN(retryAfterHeader) ? calculateRetryAfterHeader(retryAfterHeader) : retryAfterHeader * 1000;
      }
      const retryTimeout = retryAfterHeader > 0 ? Math.min(retryAfterHeader, maxTimeout) : Math.min(minTimeout * timeoutFactor ** (counter - 1), maxTimeout);
      setTimeout(() => cb(null), retryTimeout);
    }
    onHeaders(statusCode, rawHeaders, resume, statusMessage) {
      const headers = parseHeaders(rawHeaders);
      this.retryCount += 1;
      if (statusCode >= 300) {
        if (this.retryOpts.statusCodes.includes(statusCode) === false) {
          return this.handler.onHeaders(statusCode, rawHeaders, resume, statusMessage);
        } else {
          this.abort(new RequestRetryError("Request failed", statusCode, {
            headers,
            data: {
              count: this.retryCount
            }
          }));
          return false;
        }
      }
      if (this.resume != null) {
        this.resume = null;
        if (statusCode !== 206 && (this.start > 0 || statusCode !== 200)) {
          this.abort(new RequestRetryError("server does not support the range header and the payload was partially consumed", statusCode, {
            headers,
            data: { count: this.retryCount }
          }));
          return false;
        }
        const contentRange = parseRangeHeader(headers["content-range"]);
        if (!contentRange) {
          this.abort(new RequestRetryError("Content-Range mismatch", statusCode, {
            headers,
            data: { count: this.retryCount }
          }));
          return false;
        }
        if (this.etag != null && this.etag !== headers.etag) {
          this.abort(new RequestRetryError("ETag mismatch", statusCode, {
            headers,
            data: { count: this.retryCount }
          }));
          return false;
        }
        const { start, size, end = size - 1 } = contentRange;
        assert(this.start === start, "content-range mismatch");
        assert(this.end == null || this.end === end, "content-range mismatch");
        this.resume = resume;
        return true;
      }
      if (this.end == null) {
        if (statusCode === 206) {
          const range = parseRangeHeader(headers["content-range"]);
          if (range == null) {
            return this.handler.onHeaders(statusCode, rawHeaders, resume, statusMessage);
          }
          const { start, size, end = size - 1 } = range;
          assert(start != null && Number.isFinite(start), "content-range mismatch");
          assert(end != null && Number.isFinite(end), "invalid content-length");
          this.start = start;
          this.end = end;
        }
        if (this.end == null) {
          const contentLength = headers["content-length"];
          this.end = contentLength != null ? Number(contentLength) - 1 : null;
        }
        assert(Number.isFinite(this.start));
        assert(this.end == null || Number.isFinite(this.end), "invalid content-length");
        this.resume = resume;
        this.etag = headers.etag != null ? headers.etag : null;
        if (this.etag != null && this.etag.startsWith("W/")) {
          this.etag = null;
        }
        return this.handler.onHeaders(statusCode, rawHeaders, resume, statusMessage);
      }
      const err = new RequestRetryError("Request failed", statusCode, {
        headers,
        data: { count: this.retryCount }
      });
      this.abort(err);
      return false;
    }
    onData(chunk) {
      this.start += chunk.length;
      return this.handler.onData(chunk);
    }
    onComplete(rawTrailers) {
      this.retryCount = 0;
      return this.handler.onComplete(rawTrailers);
    }
    onError(err) {
      if (this.aborted || isDisturbed(this.opts.body)) {
        return this.handler.onError(err);
      }
      if (this.retryCount - this.retryCountCheckpoint > 0) {
        this.retryCount = this.retryCountCheckpoint + (this.retryCount - this.retryCountCheckpoint);
      } else {
        this.retryCount += 1;
      }
      this.retryOpts.retry(err, {
        state: { counter: this.retryCount },
        opts: { retryOptions: this.retryOpts, ...this.opts }
      }, onRetry.bind(this));
      function onRetry(err2) {
        if (err2 != null || this.aborted || isDisturbed(this.opts.body)) {
          return this.handler.onError(err2);
        }
        if (this.start !== 0) {
          const headers = { range: `bytes=${this.start}-${this.end ?? ""}` };
          if (this.etag != null) {
            headers["if-match"] = this.etag;
          }
          this.opts = {
            ...this.opts,
            headers: {
              ...this.opts.headers,
              ...headers
            }
          };
        }
        try {
          this.retryCountCheckpoint = this.retryCount;
          this.dispatch(this.opts, this);
        } catch (err3) {
          this.handler.onError(err3);
        }
      }
    }
  }
  module.exports = RetryHandler;
});

// node_modules/undici/lib/dispatcher/retry-agent.js
var require_retry_agent = __commonJS((exports, module) => {
  var Dispatcher = require_dispatcher();
  var RetryHandler = require_retry_handler();

  class RetryAgent extends Dispatcher {
    #agent = null;
    #options = null;
    constructor(agent, options = {}) {
      super(options);
      this.#agent = agent;
      this.#options = options;
    }
    dispatch(opts, handler) {
      const retry = new RetryHandler({
        ...opts,
        retryOptions: this.#options
      }, {
        dispatch: this.#agent.dispatch.bind(this.#agent),
        handler
      });
      return this.#agent.dispatch(opts, retry);
    }
    close() {
      return this.#agent.close();
    }
    destroy() {
      return this.#agent.destroy();
    }
  }
  module.exports = RetryAgent;
});

// node_modules/undici/lib/api/readable.js
var require_readable = __commonJS((exports, module) => {
  var assert = __require("assert");
  var { Readable } = __require("stream");
  var { RequestAbortedError, NotSupportedError, InvalidArgumentError, AbortError } = require_errors();
  var util = require_util();
  var { ReadableStreamFrom } = require_util();
  var kConsume = Symbol("kConsume");
  var kReading = Symbol("kReading");
  var kBody = Symbol("kBody");
  var kAbort = Symbol("kAbort");
  var kContentType = Symbol("kContentType");
  var kContentLength = Symbol("kContentLength");
  var noop = () => {};

  class BodyReadable extends Readable {
    constructor({
      resume,
      abort,
      contentType = "",
      contentLength,
      highWaterMark = 64 * 1024
    }) {
      super({
        autoDestroy: true,
        read: resume,
        highWaterMark
      });
      this._readableState.dataEmitted = false;
      this[kAbort] = abort;
      this[kConsume] = null;
      this[kBody] = null;
      this[kContentType] = contentType;
      this[kContentLength] = contentLength;
      this[kReading] = false;
    }
    destroy(err) {
      if (!err && !this._readableState.endEmitted) {
        err = new RequestAbortedError;
      }
      if (err) {
        this[kAbort]();
      }
      return super.destroy(err);
    }
    _destroy(err, callback) {
      if (!this[kReading]) {
        setImmediate(() => {
          callback(err);
        });
      } else {
        callback(err);
      }
    }
    on(ev, ...args) {
      if (ev === "data" || ev === "readable") {
        this[kReading] = true;
      }
      return super.on(ev, ...args);
    }
    addListener(ev, ...args) {
      return this.on(ev, ...args);
    }
    off(ev, ...args) {
      const ret = super.off(ev, ...args);
      if (ev === "data" || ev === "readable") {
        this[kReading] = this.listenerCount("data") > 0 || this.listenerCount("readable") > 0;
      }
      return ret;
    }
    removeListener(ev, ...args) {
      return this.off(ev, ...args);
    }
    push(chunk) {
      if (this[kConsume] && chunk !== null) {
        consumePush(this[kConsume], chunk);
        return this[kReading] ? super.push(chunk) : true;
      }
      return super.push(chunk);
    }
    async text() {
      return consume(this, "text");
    }
    async json() {
      return consume(this, "json");
    }
    async blob() {
      return consume(this, "blob");
    }
    async bytes() {
      return consume(this, "bytes");
    }
    async arrayBuffer() {
      return consume(this, "arrayBuffer");
    }
    async formData() {
      throw new NotSupportedError;
    }
    get bodyUsed() {
      return util.isDisturbed(this);
    }
    get body() {
      if (!this[kBody]) {
        this[kBody] = ReadableStreamFrom(this);
        if (this[kConsume]) {
          this[kBody].getReader();
          assert(this[kBody].locked);
        }
      }
      return this[kBody];
    }
    async dump(opts) {
      let limit = Number.isFinite(opts?.limit) ? opts.limit : 128 * 1024;
      const signal = opts?.signal;
      if (signal != null && (typeof signal !== "object" || !("aborted" in signal))) {
        throw new InvalidArgumentError("signal must be an AbortSignal");
      }
      signal?.throwIfAborted();
      if (this._readableState.closeEmitted) {
        return null;
      }
      return await new Promise((resolve3, reject) => {
        if (this[kContentLength] > limit) {
          this.destroy(new AbortError);
        }
        const onAbort = () => {
          this.destroy(signal.reason ?? new AbortError);
        };
        signal?.addEventListener("abort", onAbort);
        this.on("close", function() {
          signal?.removeEventListener("abort", onAbort);
          if (signal?.aborted) {
            reject(signal.reason ?? new AbortError);
          } else {
            resolve3(null);
          }
        }).on("error", noop).on("data", function(chunk) {
          limit -= chunk.length;
          if (limit <= 0) {
            this.destroy();
          }
        }).resume();
      });
    }
  }
  function isLocked(self) {
    return self[kBody] && self[kBody].locked === true || self[kConsume];
  }
  function isUnusable(self) {
    return util.isDisturbed(self) || isLocked(self);
  }
  async function consume(stream, type) {
    assert(!stream[kConsume]);
    return new Promise((resolve3, reject) => {
      if (isUnusable(stream)) {
        const rState = stream._readableState;
        if (rState.destroyed && rState.closeEmitted === false) {
          stream.on("error", (err) => {
            reject(err);
          }).on("close", () => {
            reject(new TypeError("unusable"));
          });
        } else {
          reject(rState.errored ?? new TypeError("unusable"));
        }
      } else {
        queueMicrotask(() => {
          stream[kConsume] = {
            type,
            stream,
            resolve: resolve3,
            reject,
            length: 0,
            body: []
          };
          stream.on("error", function(err) {
            consumeFinish(this[kConsume], err);
          }).on("close", function() {
            if (this[kConsume].body !== null) {
              consumeFinish(this[kConsume], new RequestAbortedError);
            }
          });
          consumeStart(stream[kConsume]);
        });
      }
    });
  }
  function consumeStart(consume2) {
    if (consume2.body === null) {
      return;
    }
    const { _readableState: state } = consume2.stream;
    if (state.bufferIndex) {
      const start = state.bufferIndex;
      const end = state.buffer.length;
      for (let n = start;n < end; n++) {
        consumePush(consume2, state.buffer[n]);
      }
    } else {
      for (const chunk of state.buffer) {
        consumePush(consume2, chunk);
      }
    }
    if (state.endEmitted) {
      consumeEnd(this[kConsume]);
    } else {
      consume2.stream.on("end", function() {
        consumeEnd(this[kConsume]);
      });
    }
    consume2.stream.resume();
    while (consume2.stream.read() != null) {}
  }
  function chunksDecode(chunks, length) {
    if (chunks.length === 0 || length === 0) {
      return "";
    }
    const buffer = chunks.length === 1 ? chunks[0] : Buffer.concat(chunks, length);
    const bufferLength = buffer.length;
    const start = bufferLength > 2 && buffer[0] === 239 && buffer[1] === 187 && buffer[2] === 191 ? 3 : 0;
    return buffer.utf8Slice(start, bufferLength);
  }
  function chunksConcat(chunks, length) {
    if (chunks.length === 0 || length === 0) {
      return new Uint8Array(0);
    }
    if (chunks.length === 1) {
      return new Uint8Array(chunks[0]);
    }
    const buffer = new Uint8Array(Buffer.allocUnsafeSlow(length).buffer);
    let offset = 0;
    for (let i = 0;i < chunks.length; ++i) {
      const chunk = chunks[i];
      buffer.set(chunk, offset);
      offset += chunk.length;
    }
    return buffer;
  }
  function consumeEnd(consume2) {
    const { type, body, resolve: resolve3, stream, length } = consume2;
    try {
      if (type === "text") {
        resolve3(chunksDecode(body, length));
      } else if (type === "json") {
        resolve3(JSON.parse(chunksDecode(body, length)));
      } else if (type === "arrayBuffer") {
        resolve3(chunksConcat(body, length).buffer);
      } else if (type === "blob") {
        resolve3(new Blob(body, { type: stream[kContentType] }));
      } else if (type === "bytes") {
        resolve3(chunksConcat(body, length));
      }
      consumeFinish(consume2);
    } catch (err) {
      stream.destroy(err);
    }
  }
  function consumePush(consume2, chunk) {
    consume2.length += chunk.length;
    consume2.body.push(chunk);
  }
  function consumeFinish(consume2, err) {
    if (consume2.body === null) {
      return;
    }
    if (err) {
      consume2.reject(err);
    } else {
      consume2.resolve();
    }
    consume2.type = null;
    consume2.stream = null;
    consume2.resolve = null;
    consume2.reject = null;
    consume2.length = 0;
    consume2.body = null;
  }
  module.exports = { Readable: BodyReadable, chunksDecode };
});

// node_modules/undici/lib/api/util.js
var require_util3 = __commonJS((exports, module) => {
  var assert = __require("assert");
  var {
    ResponseStatusCodeError
  } = require_errors();
  var { chunksDecode } = require_readable();
  var CHUNK_LIMIT = 128 * 1024;
  async function getResolveErrorBodyCallback({ callback, body, contentType, statusCode, statusMessage, headers }) {
    assert(body);
    let chunks = [];
    let length = 0;
    try {
      for await (const chunk of body) {
        chunks.push(chunk);
        length += chunk.length;
        if (length > CHUNK_LIMIT) {
          chunks = [];
          length = 0;
          break;
        }
      }
    } catch {
      chunks = [];
      length = 0;
    }
    const message = `Response status code ${statusCode}${statusMessage ? `: ${statusMessage}` : ""}`;
    if (statusCode === 204 || !contentType || !length) {
      queueMicrotask(() => callback(new ResponseStatusCodeError(message, statusCode, headers)));
      return;
    }
    const stackTraceLimit = Error.stackTraceLimit;
    Error.stackTraceLimit = 0;
    let payload;
    try {
      if (isContentTypeApplicationJson(contentType)) {
        payload = JSON.parse(chunksDecode(chunks, length));
      } else if (isContentTypeText(contentType)) {
        payload = chunksDecode(chunks, length);
      }
    } catch {} finally {
      Error.stackTraceLimit = stackTraceLimit;
    }
    queueMicrotask(() => callback(new ResponseStatusCodeError(message, statusCode, headers, payload)));
  }
  var isContentTypeApplicationJson = (contentType) => {
    return contentType.length > 15 && contentType[11] === "/" && contentType[0] === "a" && contentType[1] === "p" && contentType[2] === "p" && contentType[3] === "l" && contentType[4] === "i" && contentType[5] === "c" && contentType[6] === "a" && contentType[7] === "t" && contentType[8] === "i" && contentType[9] === "o" && contentType[10] === "n" && contentType[12] === "j" && contentType[13] === "s" && contentType[14] === "o" && contentType[15] === "n";
  };
  var isContentTypeText = (contentType) => {
    return contentType.length > 4 && contentType[4] === "/" && contentType[0] === "t" && contentType[1] === "e" && contentType[2] === "x" && contentType[3] === "t";
  };
  module.exports = {
    getResolveErrorBodyCallback,
    isContentTypeApplicationJson,
    isContentTypeText
  };
});

// node_modules/undici/lib/api/api-request.js
var require_api_request = __commonJS((exports, module) => {
  var assert = __require("assert");
  var { Readable } = require_readable();
  var { InvalidArgumentError, RequestAbortedError } = require_errors();
  var util = require_util();
  var { getResolveErrorBodyCallback } = require_util3();
  var { AsyncResource } = __require("async_hooks");

  class RequestHandler extends AsyncResource {
    constructor(opts, callback) {
      if (!opts || typeof opts !== "object") {
        throw new InvalidArgumentError("invalid opts");
      }
      const { signal, method, opaque, body, onInfo, responseHeaders, throwOnError, highWaterMark } = opts;
      try {
        if (typeof callback !== "function") {
          throw new InvalidArgumentError("invalid callback");
        }
        if (highWaterMark && (typeof highWaterMark !== "number" || highWaterMark < 0)) {
          throw new InvalidArgumentError("invalid highWaterMark");
        }
        if (signal && typeof signal.on !== "function" && typeof signal.addEventListener !== "function") {
          throw new InvalidArgumentError("signal must be an EventEmitter or EventTarget");
        }
        if (method === "CONNECT") {
          throw new InvalidArgumentError("invalid method");
        }
        if (onInfo && typeof onInfo !== "function") {
          throw new InvalidArgumentError("invalid onInfo callback");
        }
        super("UNDICI_REQUEST");
      } catch (err) {
        if (util.isStream(body)) {
          util.destroy(body.on("error", util.nop), err);
        }
        throw err;
      }
      this.method = method;
      this.responseHeaders = responseHeaders || null;
      this.opaque = opaque || null;
      this.callback = callback;
      this.res = null;
      this.abort = null;
      this.body = body;
      this.trailers = {};
      this.context = null;
      this.onInfo = onInfo || null;
      this.throwOnError = throwOnError;
      this.highWaterMark = highWaterMark;
      this.signal = signal;
      this.reason = null;
      this.removeAbortListener = null;
      if (util.isStream(body)) {
        body.on("error", (err) => {
          this.onError(err);
        });
      }
      if (this.signal) {
        if (this.signal.aborted) {
          this.reason = this.signal.reason ?? new RequestAbortedError;
        } else {
          this.removeAbortListener = util.addAbortListener(this.signal, () => {
            this.reason = this.signal.reason ?? new RequestAbortedError;
            if (this.res) {
              util.destroy(this.res.on("error", util.nop), this.reason);
            } else if (this.abort) {
              this.abort(this.reason);
            }
            if (this.removeAbortListener) {
              this.res?.off("close", this.removeAbortListener);
              this.removeAbortListener();
              this.removeAbortListener = null;
            }
          });
        }
      }
    }
    onConnect(abort, context) {
      if (this.reason) {
        abort(this.reason);
        return;
      }
      assert(this.callback);
      this.abort = abort;
      this.context = context;
    }
    onHeaders(statusCode, rawHeaders, resume, statusMessage) {
      const { callback, opaque, abort, context, responseHeaders, highWaterMark } = this;
      const headers = responseHeaders === "raw" ? util.parseRawHeaders(rawHeaders) : util.parseHeaders(rawHeaders);
      if (statusCode < 200) {
        if (this.onInfo) {
          this.onInfo({ statusCode, headers });
        }
        return;
      }
      const parsedHeaders = responseHeaders === "raw" ? util.parseHeaders(rawHeaders) : headers;
      const contentType = parsedHeaders["content-type"];
      const contentLength = parsedHeaders["content-length"];
      const res = new Readable({
        resume,
        abort,
        contentType,
        contentLength: this.method !== "HEAD" && contentLength ? Number(contentLength) : null,
        highWaterMark
      });
      if (this.removeAbortListener) {
        res.on("close", this.removeAbortListener);
      }
      this.callback = null;
      this.res = res;
      if (callback !== null) {
        if (this.throwOnError && statusCode >= 400) {
          this.runInAsyncScope(getResolveErrorBodyCallback, null, { callback, body: res, contentType, statusCode, statusMessage, headers });
        } else {
          this.runInAsyncScope(callback, null, null, {
            statusCode,
            headers,
            trailers: this.trailers,
            opaque,
            body: res,
            context
          });
        }
      }
    }
    onData(chunk) {
      return this.res.push(chunk);
    }
    onComplete(trailers) {
      util.parseHeaders(trailers, this.trailers);
      this.res.push(null);
    }
    onError(err) {
      const { res, callback, body, opaque } = this;
      if (callback) {
        this.callback = null;
        queueMicrotask(() => {
          this.runInAsyncScope(callback, null, err, { opaque });
        });
      }
      if (res) {
        this.res = null;
        queueMicrotask(() => {
          util.destroy(res, err);
        });
      }
      if (body) {
        this.body = null;
        util.destroy(body, err);
      }
      if (this.removeAbortListener) {
        res?.off("close", this.removeAbortListener);
        this.removeAbortListener();
        this.removeAbortListener = null;
      }
    }
  }
  function request(opts, callback) {
    if (callback === undefined) {
      return new Promise((resolve3, reject) => {
        request.call(this, opts, (err, data) => {
          return err ? reject(err) : resolve3(data);
        });
      });
    }
    try {
      this.dispatch(opts, new RequestHandler(opts, callback));
    } catch (err) {
      if (typeof callback !== "function") {
        throw err;
      }
      const opaque = opts?.opaque;
      queueMicrotask(() => callback(err, { opaque }));
    }
  }
  module.exports = request;
  module.exports.RequestHandler = RequestHandler;
});

// node_modules/undici/lib/api/abort-signal.js
var require_abort_signal = __commonJS((exports, module) => {
  var { addAbortListener } = require_util();
  var { RequestAbortedError } = require_errors();
  var kListener = Symbol("kListener");
  var kSignal = Symbol("kSignal");
  function abort(self) {
    if (self.abort) {
      self.abort(self[kSignal]?.reason);
    } else {
      self.reason = self[kSignal]?.reason ?? new RequestAbortedError;
    }
    removeSignal(self);
  }
  function addSignal(self, signal) {
    self.reason = null;
    self[kSignal] = null;
    self[kListener] = null;
    if (!signal) {
      return;
    }
    if (signal.aborted) {
      abort(self);
      return;
    }
    self[kSignal] = signal;
    self[kListener] = () => {
      abort(self);
    };
    addAbortListener(self[kSignal], self[kListener]);
  }
  function removeSignal(self) {
    if (!self[kSignal]) {
      return;
    }
    if ("removeEventListener" in self[kSignal]) {
      self[kSignal].removeEventListener("abort", self[kListener]);
    } else {
      self[kSignal].removeListener("abort", self[kListener]);
    }
    self[kSignal] = null;
    self[kListener] = null;
  }
  module.exports = {
    addSignal,
    removeSignal
  };
});

// node_modules/undici/lib/api/api-stream.js
var require_api_stream = __commonJS((exports, module) => {
  var assert = __require("assert");
  var { finished, PassThrough } = __require("stream");
  var { InvalidArgumentError, InvalidReturnValueError } = require_errors();
  var util = require_util();
  var { getResolveErrorBodyCallback } = require_util3();
  var { AsyncResource } = __require("async_hooks");
  var { addSignal, removeSignal } = require_abort_signal();

  class StreamHandler extends AsyncResource {
    constructor(opts, factory, callback) {
      if (!opts || typeof opts !== "object") {
        throw new InvalidArgumentError("invalid opts");
      }
      const { signal, method, opaque, body, onInfo, responseHeaders, throwOnError } = opts;
      try {
        if (typeof callback !== "function") {
          throw new InvalidArgumentError("invalid callback");
        }
        if (typeof factory !== "function") {
          throw new InvalidArgumentError("invalid factory");
        }
        if (signal && typeof signal.on !== "function" && typeof signal.addEventListener !== "function") {
          throw new InvalidArgumentError("signal must be an EventEmitter or EventTarget");
        }
        if (method === "CONNECT") {
          throw new InvalidArgumentError("invalid method");
        }
        if (onInfo && typeof onInfo !== "function") {
          throw new InvalidArgumentError("invalid onInfo callback");
        }
        super("UNDICI_STREAM");
      } catch (err) {
        if (util.isStream(body)) {
          util.destroy(body.on("error", util.nop), err);
        }
        throw err;
      }
      this.responseHeaders = responseHeaders || null;
      this.opaque = opaque || null;
      this.factory = factory;
      this.callback = callback;
      this.res = null;
      this.abort = null;
      this.context = null;
      this.trailers = null;
      this.body = body;
      this.onInfo = onInfo || null;
      this.throwOnError = throwOnError || false;
      if (util.isStream(body)) {
        body.on("error", (err) => {
          this.onError(err);
        });
      }
      addSignal(this, signal);
    }
    onConnect(abort, context) {
      if (this.reason) {
        abort(this.reason);
        return;
      }
      assert(this.callback);
      this.abort = abort;
      this.context = context;
    }
    onHeaders(statusCode, rawHeaders, resume, statusMessage) {
      const { factory, opaque, context, callback, responseHeaders } = this;
      const headers = responseHeaders === "raw" ? util.parseRawHeaders(rawHeaders) : util.parseHeaders(rawHeaders);
      if (statusCode < 200) {
        if (this.onInfo) {
          this.onInfo({ statusCode, headers });
        }
        return;
      }
      this.factory = null;
      let res;
      if (this.throwOnError && statusCode >= 400) {
        const parsedHeaders = responseHeaders === "raw" ? util.parseHeaders(rawHeaders) : headers;
        const contentType = parsedHeaders["content-type"];
        res = new PassThrough;
        this.callback = null;
        this.runInAsyncScope(getResolveErrorBodyCallback, null, { callback, body: res, contentType, statusCode, statusMessage, headers });
      } else {
        if (factory === null) {
          return;
        }
        res = this.runInAsyncScope(factory, null, {
          statusCode,
          headers,
          opaque,
          context
        });
        if (!res || typeof res.write !== "function" || typeof res.end !== "function" || typeof res.on !== "function") {
          throw new InvalidReturnValueError("expected Writable");
        }
        finished(res, { readable: false }, (err) => {
          const { callback: callback2, res: res2, opaque: opaque2, trailers, abort } = this;
          this.res = null;
          if (err || !res2.readable) {
            util.destroy(res2, err);
          }
          this.callback = null;
          this.runInAsyncScope(callback2, null, err || null, { opaque: opaque2, trailers });
          if (err) {
            abort();
          }
        });
      }
      res.on("drain", resume);
      this.res = res;
      const needDrain = res.writableNeedDrain !== undefined ? res.writableNeedDrain : res._writableState?.needDrain;
      return needDrain !== true;
    }
    onData(chunk) {
      const { res } = this;
      return res ? res.write(chunk) : true;
    }
    onComplete(trailers) {
      const { res } = this;
      removeSignal(this);
      if (!res) {
        return;
      }
      this.trailers = util.parseHeaders(trailers);
      res.end();
    }
    onError(err) {
      const { res, callback, opaque, body } = this;
      removeSignal(this);
      this.factory = null;
      if (res) {
        this.res = null;
        util.destroy(res, err);
      } else if (callback) {
        this.callback = null;
        queueMicrotask(() => {
          this.runInAsyncScope(callback, null, err, { opaque });
        });
      }
      if (body) {
        this.body = null;
        util.destroy(body, err);
      }
    }
  }
  function stream(opts, factory, callback) {
    if (callback === undefined) {
      return new Promise((resolve3, reject) => {
        stream.call(this, opts, factory, (err, data) => {
          return err ? reject(err) : resolve3(data);
        });
      });
    }
    try {
      this.dispatch(opts, new StreamHandler(opts, factory, callback));
    } catch (err) {
      if (typeof callback !== "function") {
        throw err;
      }
      const opaque = opts?.opaque;
      queueMicrotask(() => callback(err, { opaque }));
    }
  }
  module.exports = stream;
});

// node_modules/undici/lib/api/api-pipeline.js
var require_api_pipeline = __commonJS((exports, module) => {
  var {
    Readable,
    Duplex,
    PassThrough
  } = __require("stream");
  var {
    InvalidArgumentError,
    InvalidReturnValueError,
    RequestAbortedError
  } = require_errors();
  var util = require_util();
  var { AsyncResource } = __require("async_hooks");
  var { addSignal, removeSignal } = require_abort_signal();
  var assert = __require("assert");
  var kResume = Symbol("resume");

  class PipelineRequest extends Readable {
    constructor() {
      super({ autoDestroy: true });
      this[kResume] = null;
    }
    _read() {
      const { [kResume]: resume } = this;
      if (resume) {
        this[kResume] = null;
        resume();
      }
    }
    _destroy(err, callback) {
      this._read();
      callback(err);
    }
  }

  class PipelineResponse extends Readable {
    constructor(resume) {
      super({ autoDestroy: true });
      this[kResume] = resume;
    }
    _read() {
      this[kResume]();
    }
    _destroy(err, callback) {
      if (!err && !this._readableState.endEmitted) {
        err = new RequestAbortedError;
      }
      callback(err);
    }
  }

  class PipelineHandler extends AsyncResource {
    constructor(opts, handler) {
      if (!opts || typeof opts !== "object") {
        throw new InvalidArgumentError("invalid opts");
      }
      if (typeof handler !== "function") {
        throw new InvalidArgumentError("invalid handler");
      }
      const { signal, method, opaque, onInfo, responseHeaders } = opts;
      if (signal && typeof signal.on !== "function" && typeof signal.addEventListener !== "function") {
        throw new InvalidArgumentError("signal must be an EventEmitter or EventTarget");
      }
      if (method === "CONNECT") {
        throw new InvalidArgumentError("invalid method");
      }
      if (onInfo && typeof onInfo !== "function") {
        throw new InvalidArgumentError("invalid onInfo callback");
      }
      super("UNDICI_PIPELINE");
      this.opaque = opaque || null;
      this.responseHeaders = responseHeaders || null;
      this.handler = handler;
      this.abort = null;
      this.context = null;
      this.onInfo = onInfo || null;
      this.req = new PipelineRequest().on("error", util.nop);
      this.ret = new Duplex({
        readableObjectMode: opts.objectMode,
        autoDestroy: true,
        read: () => {
          const { body } = this;
          if (body?.resume) {
            body.resume();
          }
        },
        write: (chunk, encoding, callback) => {
          const { req } = this;
          if (req.push(chunk, encoding) || req._readableState.destroyed) {
            callback();
          } else {
            req[kResume] = callback;
          }
        },
        destroy: (err, callback) => {
          const { body, req, res, ret, abort } = this;
          if (!err && !ret._readableState.endEmitted) {
            err = new RequestAbortedError;
          }
          if (abort && err) {
            abort();
          }
          util.destroy(body, err);
          util.destroy(req, err);
          util.destroy(res, err);
          removeSignal(this);
          callback(err);
        }
      }).on("prefinish", () => {
        const { req } = this;
        req.push(null);
      });
      this.res = null;
      addSignal(this, signal);
    }
    onConnect(abort, context) {
      const { ret, res } = this;
      if (this.reason) {
        abort(this.reason);
        return;
      }
      assert(!res, "pipeline cannot be retried");
      assert(!ret.destroyed);
      this.abort = abort;
      this.context = context;
    }
    onHeaders(statusCode, rawHeaders, resume) {
      const { opaque, handler, context } = this;
      if (statusCode < 200) {
        if (this.onInfo) {
          const headers = this.responseHeaders === "raw" ? util.parseRawHeaders(rawHeaders) : util.parseHeaders(rawHeaders);
          this.onInfo({ statusCode, headers });
        }
        return;
      }
      this.res = new PipelineResponse(resume);
      let body;
      try {
        this.handler = null;
        const headers = this.responseHeaders === "raw" ? util.parseRawHeaders(rawHeaders) : util.parseHeaders(rawHeaders);
        body = this.runInAsyncScope(handler, null, {
          statusCode,
          headers,
          opaque,
          body: this.res,
          context
        });
      } catch (err) {
        this.res.on("error", util.nop);
        throw err;
      }
      if (!body || typeof body.on !== "function") {
        throw new InvalidReturnValueError("expected Readable");
      }
      body.on("data", (chunk) => {
        const { ret, body: body2 } = this;
        if (!ret.push(chunk) && body2.pause) {
          body2.pause();
        }
      }).on("error", (err) => {
        const { ret } = this;
        util.destroy(ret, err);
      }).on("end", () => {
        const { ret } = this;
        ret.push(null);
      }).on("close", () => {
        const { ret } = this;
        if (!ret._readableState.ended) {
          util.destroy(ret, new RequestAbortedError);
        }
      });
      this.body = body;
    }
    onData(chunk) {
      const { res } = this;
      return res.push(chunk);
    }
    onComplete(trailers) {
      const { res } = this;
      res.push(null);
    }
    onError(err) {
      const { ret } = this;
      this.handler = null;
      util.destroy(ret, err);
    }
  }
  function pipeline(opts, handler) {
    try {
      const pipelineHandler = new PipelineHandler(opts, handler);
      this.dispatch({ ...opts, body: pipelineHandler.req }, pipelineHandler);
      return pipelineHandler.ret;
    } catch (err) {
      return new PassThrough().destroy(err);
    }
  }
  module.exports = pipeline;
});

// node_modules/undici/lib/api/api-upgrade.js
var require_api_upgrade = __commonJS((exports, module) => {
  var { InvalidArgumentError, SocketError } = require_errors();
  var { AsyncResource } = __require("async_hooks");
  var util = require_util();
  var { addSignal, removeSignal } = require_abort_signal();
  var assert = __require("assert");

  class UpgradeHandler extends AsyncResource {
    constructor(opts, callback) {
      if (!opts || typeof opts !== "object") {
        throw new InvalidArgumentError("invalid opts");
      }
      if (typeof callback !== "function") {
        throw new InvalidArgumentError("invalid callback");
      }
      const { signal, opaque, responseHeaders } = opts;
      if (signal && typeof signal.on !== "function" && typeof signal.addEventListener !== "function") {
        throw new InvalidArgumentError("signal must be an EventEmitter or EventTarget");
      }
      super("UNDICI_UPGRADE");
      this.responseHeaders = responseHeaders || null;
      this.opaque = opaque || null;
      this.callback = callback;
      this.abort = null;
      this.context = null;
      addSignal(this, signal);
    }
    onConnect(abort, context) {
      if (this.reason) {
        abort(this.reason);
        return;
      }
      assert(this.callback);
      this.abort = abort;
      this.context = null;
    }
    onHeaders() {
      throw new SocketError("bad upgrade", null);
    }
    onUpgrade(statusCode, rawHeaders, socket) {
      assert(statusCode === 101);
      const { callback, opaque, context } = this;
      removeSignal(this);
      this.callback = null;
      const headers = this.responseHeaders === "raw" ? util.parseRawHeaders(rawHeaders) : util.parseHeaders(rawHeaders);
      this.runInAsyncScope(callback, null, null, {
        headers,
        socket,
        opaque,
        context
      });
    }
    onError(err) {
      const { callback, opaque } = this;
      removeSignal(this);
      if (callback) {
        this.callback = null;
        queueMicrotask(() => {
          this.runInAsyncScope(callback, null, err, { opaque });
        });
      }
    }
  }
  function upgrade(opts, callback) {
    if (callback === undefined) {
      return new Promise((resolve3, reject) => {
        upgrade.call(this, opts, (err, data) => {
          return err ? reject(err) : resolve3(data);
        });
      });
    }
    try {
      const upgradeHandler = new UpgradeHandler(opts, callback);
      this.dispatch({
        ...opts,
        method: opts.method || "GET",
        upgrade: opts.protocol || "Websocket"
      }, upgradeHandler);
    } catch (err) {
      if (typeof callback !== "function") {
        throw err;
      }
      const opaque = opts?.opaque;
      queueMicrotask(() => callback(err, { opaque }));
    }
  }
  module.exports = upgrade;
});

// node_modules/undici/lib/api/api-connect.js
var require_api_connect = __commonJS((exports, module) => {
  var assert = __require("assert");
  var { AsyncResource } = __require("async_hooks");
  var { InvalidArgumentError, SocketError } = require_errors();
  var util = require_util();
  var { addSignal, removeSignal } = require_abort_signal();

  class ConnectHandler extends AsyncResource {
    constructor(opts, callback) {
      if (!opts || typeof opts !== "object") {
        throw new InvalidArgumentError("invalid opts");
      }
      if (typeof callback !== "function") {
        throw new InvalidArgumentError("invalid callback");
      }
      const { signal, opaque, responseHeaders } = opts;
      if (signal && typeof signal.on !== "function" && typeof signal.addEventListener !== "function") {
        throw new InvalidArgumentError("signal must be an EventEmitter or EventTarget");
      }
      super("UNDICI_CONNECT");
      this.opaque = opaque || null;
      this.responseHeaders = responseHeaders || null;
      this.callback = callback;
      this.abort = null;
      addSignal(this, signal);
    }
    onConnect(abort, context) {
      if (this.reason) {
        abort(this.reason);
        return;
      }
      assert(this.callback);
      this.abort = abort;
      this.context = context;
    }
    onHeaders() {
      throw new SocketError("bad connect", null);
    }
    onUpgrade(statusCode, rawHeaders, socket) {
      const { callback, opaque, context } = this;
      removeSignal(this);
      this.callback = null;
      let headers = rawHeaders;
      if (headers != null) {
        headers = this.responseHeaders === "raw" ? util.parseRawHeaders(rawHeaders) : util.parseHeaders(rawHeaders);
      }
      this.runInAsyncScope(callback, null, null, {
        statusCode,
        headers,
        socket,
        opaque,
        context
      });
    }
    onError(err) {
      const { callback, opaque } = this;
      removeSignal(this);
      if (callback) {
        this.callback = null;
        queueMicrotask(() => {
          this.runInAsyncScope(callback, null, err, { opaque });
        });
      }
    }
  }
  function connect(opts, callback) {
    if (callback === undefined) {
      return new Promise((resolve3, reject) => {
        connect.call(this, opts, (err, data) => {
          return err ? reject(err) : resolve3(data);
        });
      });
    }
    try {
      const connectHandler = new ConnectHandler(opts, callback);
      this.dispatch({ ...opts, method: "CONNECT" }, connectHandler);
    } catch (err) {
      if (typeof callback !== "function") {
        throw err;
      }
      const opaque = opts?.opaque;
      queueMicrotask(() => callback(err, { opaque }));
    }
  }
  module.exports = connect;
});

// node_modules/undici/lib/api/index.js
var require_api = __commonJS((exports, module) => {
  exports.request = require_api_request();
  exports.stream = require_api_stream();
  exports.pipeline = require_api_pipeline();
  exports.upgrade = require_api_upgrade();
  exports.connect = require_api_connect();
});

// node_modules/undici/lib/mock/mock-errors.js
var require_mock_errors = __commonJS((exports, module) => {
  var { UndiciError } = require_errors();
  var kMockNotMatchedError = Symbol.for("undici.error.UND_MOCK_ERR_MOCK_NOT_MATCHED");

  class MockNotMatchedError extends UndiciError {
    constructor(message) {
      super(message);
      Error.captureStackTrace(this, MockNotMatchedError);
      this.name = "MockNotMatchedError";
      this.message = message || "The request does not match any registered mock dispatches";
      this.code = "UND_MOCK_ERR_MOCK_NOT_MATCHED";
    }
    static [Symbol.hasInstance](instance) {
      return instance && instance[kMockNotMatchedError] === true;
    }
    [kMockNotMatchedError] = true;
  }
  module.exports = {
    MockNotMatchedError
  };
});

// node_modules/undici/lib/mock/mock-symbols.js
var require_mock_symbols = __commonJS((exports, module) => {
  module.exports = {
    kAgent: Symbol("agent"),
    kOptions: Symbol("options"),
    kFactory: Symbol("factory"),
    kDispatches: Symbol("dispatches"),
    kDispatchKey: Symbol("dispatch key"),
    kDefaultHeaders: Symbol("default headers"),
    kDefaultTrailers: Symbol("default trailers"),
    kContentLength: Symbol("content length"),
    kMockAgent: Symbol("mock agent"),
    kMockAgentSet: Symbol("mock agent set"),
    kMockAgentGet: Symbol("mock agent get"),
    kMockDispatch: Symbol("mock dispatch"),
    kClose: Symbol("close"),
    kOriginalClose: Symbol("original agent close"),
    kOrigin: Symbol("origin"),
    kIsMockActive: Symbol("is mock active"),
    kNetConnect: Symbol("net connect"),
    kGetNetConnect: Symbol("get net connect"),
    kConnected: Symbol("connected")
  };
});

// node_modules/undici/lib/mock/mock-utils.js
var require_mock_utils = __commonJS((exports, module) => {
  var { MockNotMatchedError } = require_mock_errors();
  var {
    kDispatches,
    kMockAgent,
    kOriginalDispatch,
    kOrigin,
    kGetNetConnect
  } = require_mock_symbols();
  var { buildURL } = require_util();
  var { STATUS_CODES } = __require("http");
  var {
    types: {
      isPromise
    }
  } = __require("util");
  function matchValue(match, value) {
    if (typeof match === "string") {
      return match === value;
    }
    if (match instanceof RegExp) {
      return match.test(value);
    }
    if (typeof match === "function") {
      return match(value) === true;
    }
    return false;
  }
  function lowerCaseEntries(headers) {
    return Object.fromEntries(Object.entries(headers).map(([headerName, headerValue]) => {
      return [headerName.toLocaleLowerCase(), headerValue];
    }));
  }
  function getHeaderByName(headers, key) {
    if (Array.isArray(headers)) {
      for (let i = 0;i < headers.length; i += 2) {
        if (headers[i].toLocaleLowerCase() === key.toLocaleLowerCase()) {
          return headers[i + 1];
        }
      }
      return;
    } else if (typeof headers.get === "function") {
      return headers.get(key);
    } else {
      return lowerCaseEntries(headers)[key.toLocaleLowerCase()];
    }
  }
  function buildHeadersFromArray(headers) {
    const clone = headers.slice();
    const entries = [];
    for (let index = 0;index < clone.length; index += 2) {
      entries.push([clone[index], clone[index + 1]]);
    }
    return Object.fromEntries(entries);
  }
  function matchHeaders(mockDispatch2, headers) {
    if (typeof mockDispatch2.headers === "function") {
      if (Array.isArray(headers)) {
        headers = buildHeadersFromArray(headers);
      }
      return mockDispatch2.headers(headers ? lowerCaseEntries(headers) : {});
    }
    if (typeof mockDispatch2.headers === "undefined") {
      return true;
    }
    if (typeof headers !== "object" || typeof mockDispatch2.headers !== "object") {
      return false;
    }
    for (const [matchHeaderName, matchHeaderValue] of Object.entries(mockDispatch2.headers)) {
      const headerValue = getHeaderByName(headers, matchHeaderName);
      if (!matchValue(matchHeaderValue, headerValue)) {
        return false;
      }
    }
    return true;
  }
  function safeUrl(path) {
    if (typeof path !== "string") {
      return path;
    }
    const pathSegments = path.split("?");
    if (pathSegments.length !== 2) {
      return path;
    }
    const qp = new URLSearchParams(pathSegments.pop());
    qp.sort();
    return [...pathSegments, qp.toString()].join("?");
  }
  function matchKey(mockDispatch2, { path, method, body, headers }) {
    const pathMatch = matchValue(mockDispatch2.path, path);
    const methodMatch = matchValue(mockDispatch2.method, method);
    const bodyMatch = typeof mockDispatch2.body !== "undefined" ? matchValue(mockDispatch2.body, body) : true;
    const headersMatch = matchHeaders(mockDispatch2, headers);
    return pathMatch && methodMatch && bodyMatch && headersMatch;
  }
  function getResponseData(data) {
    if (Buffer.isBuffer(data)) {
      return data;
    } else if (data instanceof Uint8Array) {
      return data;
    } else if (data instanceof ArrayBuffer) {
      return data;
    } else if (typeof data === "object") {
      return JSON.stringify(data);
    } else {
      return data.toString();
    }
  }
  function getMockDispatch(mockDispatches, key) {
    const basePath = key.query ? buildURL(key.path, key.query) : key.path;
    const resolvedPath = typeof basePath === "string" ? safeUrl(basePath) : basePath;
    let matchedMockDispatches = mockDispatches.filter(({ consumed }) => !consumed).filter(({ path }) => matchValue(safeUrl(path), resolvedPath));
    if (matchedMockDispatches.length === 0) {
      throw new MockNotMatchedError(`Mock dispatch not matched for path '${resolvedPath}'`);
    }
    matchedMockDispatches = matchedMockDispatches.filter(({ method }) => matchValue(method, key.method));
    if (matchedMockDispatches.length === 0) {
      throw new MockNotMatchedError(`Mock dispatch not matched for method '${key.method}' on path '${resolvedPath}'`);
    }
    matchedMockDispatches = matchedMockDispatches.filter(({ body }) => typeof body !== "undefined" ? matchValue(body, key.body) : true);
    if (matchedMockDispatches.length === 0) {
      throw new MockNotMatchedError(`Mock dispatch not matched for body '${key.body}' on path '${resolvedPath}'`);
    }
    matchedMockDispatches = matchedMockDispatches.filter((mockDispatch2) => matchHeaders(mockDispatch2, key.headers));
    if (matchedMockDispatches.length === 0) {
      const headers = typeof key.headers === "object" ? JSON.stringify(key.headers) : key.headers;
      throw new MockNotMatchedError(`Mock dispatch not matched for headers '${headers}' on path '${resolvedPath}'`);
    }
    return matchedMockDispatches[0];
  }
  function addMockDispatch(mockDispatches, key, data) {
    const baseData = { timesInvoked: 0, times: 1, persist: false, consumed: false };
    const replyData = typeof data === "function" ? { callback: data } : { ...data };
    const newMockDispatch = { ...baseData, ...key, pending: true, data: { error: null, ...replyData } };
    mockDispatches.push(newMockDispatch);
    return newMockDispatch;
  }
  function deleteMockDispatch(mockDispatches, key) {
    const index = mockDispatches.findIndex((dispatch) => {
      if (!dispatch.consumed) {
        return false;
      }
      return matchKey(dispatch, key);
    });
    if (index !== -1) {
      mockDispatches.splice(index, 1);
    }
  }
  function buildKey(opts) {
    const { path, method, body, headers, query } = opts;
    return {
      path,
      method,
      body,
      headers,
      query
    };
  }
  function generateKeyValues(data) {
    const keys = Object.keys(data);
    const result = [];
    for (let i = 0;i < keys.length; ++i) {
      const key = keys[i];
      const value = data[key];
      const name = Buffer.from(`${key}`);
      if (Array.isArray(value)) {
        for (let j = 0;j < value.length; ++j) {
          result.push(name, Buffer.from(`${value[j]}`));
        }
      } else {
        result.push(name, Buffer.from(`${value}`));
      }
    }
    return result;
  }
  function getStatusText(statusCode) {
    return STATUS_CODES[statusCode] || "unknown";
  }
  async function getResponse(body) {
    const buffers = [];
    for await (const data of body) {
      buffers.push(data);
    }
    return Buffer.concat(buffers).toString("utf8");
  }
  function mockDispatch(opts, handler) {
    const key = buildKey(opts);
    const mockDispatch2 = getMockDispatch(this[kDispatches], key);
    mockDispatch2.timesInvoked++;
    if (mockDispatch2.data.callback) {
      mockDispatch2.data = { ...mockDispatch2.data, ...mockDispatch2.data.callback(opts) };
    }
    const { data: { statusCode, data, headers, trailers, error }, delay, persist } = mockDispatch2;
    const { timesInvoked, times } = mockDispatch2;
    mockDispatch2.consumed = !persist && timesInvoked >= times;
    mockDispatch2.pending = timesInvoked < times;
    if (error !== null) {
      deleteMockDispatch(this[kDispatches], key);
      handler.onError(error);
      return true;
    }
    if (typeof delay === "number" && delay > 0) {
      setTimeout(() => {
        handleReply(this[kDispatches]);
      }, delay);
    } else {
      handleReply(this[kDispatches]);
    }
    function handleReply(mockDispatches, _data = data) {
      const optsHeaders = Array.isArray(opts.headers) ? buildHeadersFromArray(opts.headers) : opts.headers;
      const body = typeof _data === "function" ? _data({ ...opts, headers: optsHeaders }) : _data;
      if (isPromise(body)) {
        body.then((newData) => handleReply(mockDispatches, newData));
        return;
      }
      const responseData = getResponseData(body);
      const responseHeaders = generateKeyValues(headers);
      const responseTrailers = generateKeyValues(trailers);
      handler.onConnect?.((err) => handler.onError(err), null);
      handler.onHeaders?.(statusCode, responseHeaders, resume, getStatusText(statusCode));
      handler.onData?.(Buffer.from(responseData));
      handler.onComplete?.(responseTrailers);
      deleteMockDispatch(mockDispatches, key);
    }
    function resume() {}
    return true;
  }
  function buildMockDispatch() {
    const agent = this[kMockAgent];
    const origin = this[kOrigin];
    const originalDispatch = this[kOriginalDispatch];
    return function dispatch(opts, handler) {
      if (agent.isMockActive) {
        try {
          mockDispatch.call(this, opts, handler);
        } catch (error) {
          if (error instanceof MockNotMatchedError) {
            const netConnect = agent[kGetNetConnect]();
            if (netConnect === false) {
              throw new MockNotMatchedError(`${error.message}: subsequent request to origin ${origin} was not allowed (net.connect disabled)`);
            }
            if (checkNetConnect(netConnect, origin)) {
              originalDispatch.call(this, opts, handler);
            } else {
              throw new MockNotMatchedError(`${error.message}: subsequent request to origin ${origin} was not allowed (net.connect is not enabled for this origin)`);
            }
          } else {
            throw error;
          }
        }
      } else {
        originalDispatch.call(this, opts, handler);
      }
    };
  }
  function checkNetConnect(netConnect, origin) {
    const url = new URL(origin);
    if (netConnect === true) {
      return true;
    } else if (Array.isArray(netConnect) && netConnect.some((matcher) => matchValue(matcher, url.host))) {
      return true;
    }
    return false;
  }
  function buildMockOptions(opts) {
    if (opts) {
      const { agent, ...mockOptions } = opts;
      return mockOptions;
    }
  }
  module.exports = {
    getResponseData,
    getMockDispatch,
    addMockDispatch,
    deleteMockDispatch,
    buildKey,
    generateKeyValues,
    matchValue,
    getResponse,
    getStatusText,
    mockDispatch,
    buildMockDispatch,
    checkNetConnect,
    buildMockOptions,
    getHeaderByName,
    buildHeadersFromArray
  };
});

// node_modules/undici/lib/mock/mock-interceptor.js
var require_mock_interceptor = __commonJS((exports, module) => {
  var { getResponseData, buildKey, addMockDispatch } = require_mock_utils();
  var {
    kDispatches,
    kDispatchKey,
    kDefaultHeaders,
    kDefaultTrailers,
    kContentLength,
    kMockDispatch
  } = require_mock_symbols();
  var { InvalidArgumentError } = require_errors();
  var { buildURL } = require_util();

  class MockScope {
    constructor(mockDispatch) {
      this[kMockDispatch] = mockDispatch;
    }
    delay(waitInMs) {
      if (typeof waitInMs !== "number" || !Number.isInteger(waitInMs) || waitInMs <= 0) {
        throw new InvalidArgumentError("waitInMs must be a valid integer > 0");
      }
      this[kMockDispatch].delay = waitInMs;
      return this;
    }
    persist() {
      this[kMockDispatch].persist = true;
      return this;
    }
    times(repeatTimes) {
      if (typeof repeatTimes !== "number" || !Number.isInteger(repeatTimes) || repeatTimes <= 0) {
        throw new InvalidArgumentError("repeatTimes must be a valid integer > 0");
      }
      this[kMockDispatch].times = repeatTimes;
      return this;
    }
  }

  class MockInterceptor {
    constructor(opts, mockDispatches) {
      if (typeof opts !== "object") {
        throw new InvalidArgumentError("opts must be an object");
      }
      if (typeof opts.path === "undefined") {
        throw new InvalidArgumentError("opts.path must be defined");
      }
      if (typeof opts.method === "undefined") {
        opts.method = "GET";
      }
      if (typeof opts.path === "string") {
        if (opts.query) {
          opts.path = buildURL(opts.path, opts.query);
        } else {
          const parsedURL = new URL(opts.path, "data://");
          opts.path = parsedURL.pathname + parsedURL.search;
        }
      }
      if (typeof opts.method === "string") {
        opts.method = opts.method.toUpperCase();
      }
      this[kDispatchKey] = buildKey(opts);
      this[kDispatches] = mockDispatches;
      this[kDefaultHeaders] = {};
      this[kDefaultTrailers] = {};
      this[kContentLength] = false;
    }
    createMockScopeDispatchData({ statusCode, data, responseOptions }) {
      const responseData = getResponseData(data);
      const contentLength = this[kContentLength] ? { "content-length": responseData.length } : {};
      const headers = { ...this[kDefaultHeaders], ...contentLength, ...responseOptions.headers };
      const trailers = { ...this[kDefaultTrailers], ...responseOptions.trailers };
      return { statusCode, data, headers, trailers };
    }
    validateReplyParameters(replyParameters) {
      if (typeof replyParameters.statusCode === "undefined") {
        throw new InvalidArgumentError("statusCode must be defined");
      }
      if (typeof replyParameters.responseOptions !== "object" || replyParameters.responseOptions === null) {
        throw new InvalidArgumentError("responseOptions must be an object");
      }
    }
    reply(replyOptionsCallbackOrStatusCode) {
      if (typeof replyOptionsCallbackOrStatusCode === "function") {
        const wrappedDefaultsCallback = (opts) => {
          const resolvedData = replyOptionsCallbackOrStatusCode(opts);
          if (typeof resolvedData !== "object" || resolvedData === null) {
            throw new InvalidArgumentError("reply options callback must return an object");
          }
          const replyParameters2 = { data: "", responseOptions: {}, ...resolvedData };
          this.validateReplyParameters(replyParameters2);
          return {
            ...this.createMockScopeDispatchData(replyParameters2)
          };
        };
        const newMockDispatch2 = addMockDispatch(this[kDispatches], this[kDispatchKey], wrappedDefaultsCallback);
        return new MockScope(newMockDispatch2);
      }
      const replyParameters = {
        statusCode: replyOptionsCallbackOrStatusCode,
        data: arguments[1] === undefined ? "" : arguments[1],
        responseOptions: arguments[2] === undefined ? {} : arguments[2]
      };
      this.validateReplyParameters(replyParameters);
      const dispatchData = this.createMockScopeDispatchData(replyParameters);
      const newMockDispatch = addMockDispatch(this[kDispatches], this[kDispatchKey], dispatchData);
      return new MockScope(newMockDispatch);
    }
    replyWithError(error) {
      if (typeof error === "undefined") {
        throw new InvalidArgumentError("error must be defined");
      }
      const newMockDispatch = addMockDispatch(this[kDispatches], this[kDispatchKey], { error });
      return new MockScope(newMockDispatch);
    }
    defaultReplyHeaders(headers) {
      if (typeof headers === "undefined") {
        throw new InvalidArgumentError("headers must be defined");
      }
      this[kDefaultHeaders] = headers;
      return this;
    }
    defaultReplyTrailers(trailers) {
      if (typeof trailers === "undefined") {
        throw new InvalidArgumentError("trailers must be defined");
      }
      this[kDefaultTrailers] = trailers;
      return this;
    }
    replyContentLength() {
      this[kContentLength] = true;
      return this;
    }
  }
  exports.MockInterceptor = MockInterceptor;
  exports.MockScope = MockScope;
});

// node_modules/undici/lib/mock/mock-client.js
var require_mock_client = __commonJS((exports, module) => {
  var { promisify } = __require("util");
  var Client = require_client();
  var { buildMockDispatch } = require_mock_utils();
  var {
    kDispatches,
    kMockAgent,
    kClose,
    kOriginalClose,
    kOrigin,
    kOriginalDispatch,
    kConnected
  } = require_mock_symbols();
  var { MockInterceptor } = require_mock_interceptor();
  var Symbols = require_symbols();
  var { InvalidArgumentError } = require_errors();

  class MockClient extends Client {
    constructor(origin, opts) {
      super(origin, opts);
      if (!opts || !opts.agent || typeof opts.agent.dispatch !== "function") {
        throw new InvalidArgumentError("Argument opts.agent must implement Agent");
      }
      this[kMockAgent] = opts.agent;
      this[kOrigin] = origin;
      this[kDispatches] = [];
      this[kConnected] = 1;
      this[kOriginalDispatch] = this.dispatch;
      this[kOriginalClose] = this.close.bind(this);
      this.dispatch = buildMockDispatch.call(this);
      this.close = this[kClose];
    }
    get [Symbols.kConnected]() {
      return this[kConnected];
    }
    intercept(opts) {
      return new MockInterceptor(opts, this[kDispatches]);
    }
    async[kClose]() {
      await promisify(this[kOriginalClose])();
      this[kConnected] = 0;
      this[kMockAgent][Symbols.kClients].delete(this[kOrigin]);
    }
  }
  module.exports = MockClient;
});

// node_modules/undici/lib/mock/mock-pool.js
var require_mock_pool = __commonJS((exports, module) => {
  var { promisify } = __require("util");
  var Pool = require_pool();
  var { buildMockDispatch } = require_mock_utils();
  var {
    kDispatches,
    kMockAgent,
    kClose,
    kOriginalClose,
    kOrigin,
    kOriginalDispatch,
    kConnected
  } = require_mock_symbols();
  var { MockInterceptor } = require_mock_interceptor();
  var Symbols = require_symbols();
  var { InvalidArgumentError } = require_errors();

  class MockPool extends Pool {
    constructor(origin, opts) {
      super(origin, opts);
      if (!opts || !opts.agent || typeof opts.agent.dispatch !== "function") {
        throw new InvalidArgumentError("Argument opts.agent must implement Agent");
      }
      this[kMockAgent] = opts.agent;
      this[kOrigin] = origin;
      this[kDispatches] = [];
      this[kConnected] = 1;
      this[kOriginalDispatch] = this.dispatch;
      this[kOriginalClose] = this.close.bind(this);
      this.dispatch = buildMockDispatch.call(this);
      this.close = this[kClose];
    }
    get [Symbols.kConnected]() {
      return this[kConnected];
    }
    intercept(opts) {
      return new MockInterceptor(opts, this[kDispatches]);
    }
    async[kClose]() {
      await promisify(this[kOriginalClose])();
      this[kConnected] = 0;
      this[kMockAgent][Symbols.kClients].delete(this[kOrigin]);
    }
  }
  module.exports = MockPool;
});

// node_modules/undici/lib/mock/pluralizer.js
var require_pluralizer = __commonJS((exports, module) => {
  var singulars = {
    pronoun: "it",
    is: "is",
    was: "was",
    this: "this"
  };
  var plurals = {
    pronoun: "they",
    is: "are",
    was: "were",
    this: "these"
  };
  module.exports = class Pluralizer {
    constructor(singular, plural) {
      this.singular = singular;
      this.plural = plural;
    }
    pluralize(count) {
      const one = count === 1;
      const keys = one ? singulars : plurals;
      const noun = one ? this.singular : this.plural;
      return { ...keys, count, noun };
    }
  };
});

// node_modules/undici/lib/mock/pending-interceptors-formatter.js
var require_pending_interceptors_formatter = __commonJS((exports, module) => {
  var { Transform } = __require("stream");
  var { Console } = __require("console");
  var PERSISTENT = process.versions.icu ? "\u2705" : "Y ";
  var NOT_PERSISTENT = process.versions.icu ? "\u274C" : "N ";
  module.exports = class PendingInterceptorsFormatter {
    constructor({ disableColors } = {}) {
      this.transform = new Transform({
        transform(chunk, _enc, cb) {
          cb(null, chunk);
        }
      });
      this.logger = new Console({
        stdout: this.transform,
        inspectOptions: {
          colors: !disableColors && !process.env.CI
        }
      });
    }
    format(pendingInterceptors) {
      const withPrettyHeaders = pendingInterceptors.map(({ method, path, data: { statusCode }, persist, times, timesInvoked, origin }) => ({
        Method: method,
        Origin: origin,
        Path: path,
        "Status code": statusCode,
        Persistent: persist ? PERSISTENT : NOT_PERSISTENT,
        Invocations: timesInvoked,
        Remaining: persist ? Infinity : times - timesInvoked
      }));
      this.logger.table(withPrettyHeaders);
      return this.transform.read().toString();
    }
  };
});

// node_modules/undici/lib/mock/mock-agent.js
var require_mock_agent = __commonJS((exports, module) => {
  var { kClients } = require_symbols();
  var Agent = require_agent();
  var {
    kAgent,
    kMockAgentSet,
    kMockAgentGet,
    kDispatches,
    kIsMockActive,
    kNetConnect,
    kGetNetConnect,
    kOptions,
    kFactory
  } = require_mock_symbols();
  var MockClient = require_mock_client();
  var MockPool = require_mock_pool();
  var { matchValue, buildMockOptions } = require_mock_utils();
  var { InvalidArgumentError, UndiciError } = require_errors();
  var Dispatcher = require_dispatcher();
  var Pluralizer = require_pluralizer();
  var PendingInterceptorsFormatter = require_pending_interceptors_formatter();

  class MockAgent extends Dispatcher {
    constructor(opts) {
      super(opts);
      this[kNetConnect] = true;
      this[kIsMockActive] = true;
      if (opts?.agent && typeof opts.agent.dispatch !== "function") {
        throw new InvalidArgumentError("Argument opts.agent must implement Agent");
      }
      const agent = opts?.agent ? opts.agent : new Agent(opts);
      this[kAgent] = agent;
      this[kClients] = agent[kClients];
      this[kOptions] = buildMockOptions(opts);
    }
    get(origin) {
      let dispatcher = this[kMockAgentGet](origin);
      if (!dispatcher) {
        dispatcher = this[kFactory](origin);
        this[kMockAgentSet](origin, dispatcher);
      }
      return dispatcher;
    }
    dispatch(opts, handler) {
      this.get(opts.origin);
      return this[kAgent].dispatch(opts, handler);
    }
    async close() {
      await this[kAgent].close();
      this[kClients].clear();
    }
    deactivate() {
      this[kIsMockActive] = false;
    }
    activate() {
      this[kIsMockActive] = true;
    }
    enableNetConnect(matcher) {
      if (typeof matcher === "string" || typeof matcher === "function" || matcher instanceof RegExp) {
        if (Array.isArray(this[kNetConnect])) {
          this[kNetConnect].push(matcher);
        } else {
          this[kNetConnect] = [matcher];
        }
      } else if (typeof matcher === "undefined") {
        this[kNetConnect] = true;
      } else {
        throw new InvalidArgumentError("Unsupported matcher. Must be one of String|Function|RegExp.");
      }
    }
    disableNetConnect() {
      this[kNetConnect] = false;
    }
    get isMockActive() {
      return this[kIsMockActive];
    }
    [kMockAgentSet](origin, dispatcher) {
      this[kClients].set(origin, dispatcher);
    }
    [kFactory](origin) {
      const mockOptions = Object.assign({ agent: this }, this[kOptions]);
      return this[kOptions] && this[kOptions].connections === 1 ? new MockClient(origin, mockOptions) : new MockPool(origin, mockOptions);
    }
    [kMockAgentGet](origin) {
      const client = this[kClients].get(origin);
      if (client) {
        return client;
      }
      if (typeof origin !== "string") {
        const dispatcher = this[kFactory]("http://localhost:9999");
        this[kMockAgentSet](origin, dispatcher);
        return dispatcher;
      }
      for (const [keyMatcher, nonExplicitDispatcher] of Array.from(this[kClients])) {
        if (nonExplicitDispatcher && typeof keyMatcher !== "string" && matchValue(keyMatcher, origin)) {
          const dispatcher = this[kFactory](origin);
          this[kMockAgentSet](origin, dispatcher);
          dispatcher[kDispatches] = nonExplicitDispatcher[kDispatches];
          return dispatcher;
        }
      }
    }
    [kGetNetConnect]() {
      return this[kNetConnect];
    }
    pendingInterceptors() {
      const mockAgentClients = this[kClients];
      return Array.from(mockAgentClients.entries()).flatMap(([origin, scope]) => scope[kDispatches].map((dispatch) => ({ ...dispatch, origin }))).filter(({ pending }) => pending);
    }
    assertNoPendingInterceptors({ pendingInterceptorsFormatter = new PendingInterceptorsFormatter } = {}) {
      const pending = this.pendingInterceptors();
      if (pending.length === 0) {
        return;
      }
      const pluralizer = new Pluralizer("interceptor", "interceptors").pluralize(pending.length);
      throw new UndiciError(`
${pluralizer.count} ${pluralizer.noun} ${pluralizer.is} pending:

${pendingInterceptorsFormatter.format(pending)}
`.trim());
    }
  }
  module.exports = MockAgent;
});

// node_modules/undici/lib/global.js
var require_global2 = __commonJS((exports, module) => {
  var globalDispatcher = Symbol.for("undici.globalDispatcher.1");
  var { InvalidArgumentError } = require_errors();
  var Agent = require_agent();
  if (getGlobalDispatcher() === undefined) {
    setGlobalDispatcher(new Agent);
  }
  function setGlobalDispatcher(agent) {
    if (!agent || typeof agent.dispatch !== "function") {
      throw new InvalidArgumentError("Argument agent must implement Agent");
    }
    Object.defineProperty(globalThis, globalDispatcher, {
      value: agent,
      writable: true,
      enumerable: false,
      configurable: false
    });
  }
  function getGlobalDispatcher() {
    return globalThis[globalDispatcher];
  }
  module.exports = {
    setGlobalDispatcher,
    getGlobalDispatcher
  };
});

// node_modules/undici/lib/handler/decorator-handler.js
var require_decorator_handler = __commonJS((exports, module) => {
  module.exports = class DecoratorHandler {
    #handler;
    constructor(handler) {
      if (typeof handler !== "object" || handler === null) {
        throw new TypeError("handler must be an object");
      }
      this.#handler = handler;
    }
    onConnect(...args) {
      return this.#handler.onConnect?.(...args);
    }
    onError(...args) {
      return this.#handler.onError?.(...args);
    }
    onUpgrade(...args) {
      return this.#handler.onUpgrade?.(...args);
    }
    onResponseStarted(...args) {
      return this.#handler.onResponseStarted?.(...args);
    }
    onHeaders(...args) {
      return this.#handler.onHeaders?.(...args);
    }
    onData(...args) {
      return this.#handler.onData?.(...args);
    }
    onComplete(...args) {
      return this.#handler.onComplete?.(...args);
    }
    onBodySent(...args) {
      return this.#handler.onBodySent?.(...args);
    }
  };
});

// node_modules/undici/lib/interceptor/redirect.js
var require_redirect = __commonJS((exports, module) => {
  var RedirectHandler = require_redirect_handler();
  module.exports = (opts) => {
    const globalMaxRedirections = opts?.maxRedirections;
    return (dispatch) => {
      return function redirectInterceptor(opts2, handler) {
        const { maxRedirections = globalMaxRedirections, ...baseOpts } = opts2;
        if (!maxRedirections) {
          return dispatch(opts2, handler);
        }
        const redirectHandler = new RedirectHandler(dispatch, maxRedirections, opts2, handler);
        return dispatch(baseOpts, redirectHandler);
      };
    };
  };
});

// node_modules/undici/lib/interceptor/retry.js
var require_retry = __commonJS((exports, module) => {
  var RetryHandler = require_retry_handler();
  module.exports = (globalOpts) => {
    return (dispatch) => {
      return function retryInterceptor(opts, handler) {
        return dispatch(opts, new RetryHandler({ ...opts, retryOptions: { ...globalOpts, ...opts.retryOptions } }, {
          handler,
          dispatch
        }));
      };
    };
  };
});

// node_modules/undici/lib/interceptor/dump.js
var require_dump = __commonJS((exports, module) => {
  var util = require_util();
  var { InvalidArgumentError, RequestAbortedError } = require_errors();
  var DecoratorHandler = require_decorator_handler();

  class DumpHandler extends DecoratorHandler {
    #maxSize = 1024 * 1024;
    #abort = null;
    #dumped = false;
    #aborted = false;
    #size = 0;
    #reason = null;
    #handler = null;
    constructor({ maxSize }, handler) {
      super(handler);
      if (maxSize != null && (!Number.isFinite(maxSize) || maxSize < 1)) {
        throw new InvalidArgumentError("maxSize must be a number greater than 0");
      }
      this.#maxSize = maxSize ?? this.#maxSize;
      this.#handler = handler;
    }
    onConnect(abort) {
      this.#abort = abort;
      this.#handler.onConnect(this.#customAbort.bind(this));
    }
    #customAbort(reason) {
      this.#aborted = true;
      this.#reason = reason;
    }
    onHeaders(statusCode, rawHeaders, resume, statusMessage) {
      const headers = util.parseHeaders(rawHeaders);
      const contentLength = headers["content-length"];
      if (contentLength != null && contentLength > this.#maxSize) {
        throw new RequestAbortedError(`Response size (${contentLength}) larger than maxSize (${this.#maxSize})`);
      }
      if (this.#aborted) {
        return true;
      }
      return this.#handler.onHeaders(statusCode, rawHeaders, resume, statusMessage);
    }
    onError(err) {
      if (this.#dumped) {
        return;
      }
      err = this.#reason ?? err;
      this.#handler.onError(err);
    }
    onData(chunk) {
      this.#size = this.#size + chunk.length;
      if (this.#size >= this.#maxSize) {
        this.#dumped = true;
        if (this.#aborted) {
          this.#handler.onError(this.#reason);
        } else {
          this.#handler.onComplete([]);
        }
      }
      return true;
    }
    onComplete(trailers) {
      if (this.#dumped) {
        return;
      }
      if (this.#aborted) {
        this.#handler.onError(this.reason);
        return;
      }
      this.#handler.onComplete(trailers);
    }
  }
  function createDumpInterceptor({ maxSize: defaultMaxSize } = {
    maxSize: 1024 * 1024
  }) {
    return (dispatch) => {
      return function Intercept(opts, handler) {
        const { dumpMaxSize = defaultMaxSize } = opts;
        const dumpHandler = new DumpHandler({ maxSize: dumpMaxSize }, handler);
        return dispatch(opts, dumpHandler);
      };
    };
  }
  module.exports = createDumpInterceptor;
});

// node_modules/undici/lib/interceptor/dns.js
var require_dns = __commonJS((exports, module) => {
  var { isIP } = __require("net");
  var { lookup } = __require("dns");
  var DecoratorHandler = require_decorator_handler();
  var { InvalidArgumentError, InformationalError } = require_errors();
  var maxInt = Math.pow(2, 31) - 1;

  class DNSInstance {
    #maxTTL = 0;
    #maxItems = 0;
    #records = new Map;
    dualStack = true;
    affinity = null;
    lookup = null;
    pick = null;
    constructor(opts) {
      this.#maxTTL = opts.maxTTL;
      this.#maxItems = opts.maxItems;
      this.dualStack = opts.dualStack;
      this.affinity = opts.affinity;
      this.lookup = opts.lookup ?? this.#defaultLookup;
      this.pick = opts.pick ?? this.#defaultPick;
    }
    get full() {
      return this.#records.size === this.#maxItems;
    }
    runLookup(origin, opts, cb) {
      const ips = this.#records.get(origin.hostname);
      if (ips == null && this.full) {
        cb(null, origin.origin);
        return;
      }
      const newOpts = {
        affinity: this.affinity,
        dualStack: this.dualStack,
        lookup: this.lookup,
        pick: this.pick,
        ...opts.dns,
        maxTTL: this.#maxTTL,
        maxItems: this.#maxItems
      };
      if (ips == null) {
        this.lookup(origin, newOpts, (err, addresses) => {
          if (err || addresses == null || addresses.length === 0) {
            cb(err ?? new InformationalError("No DNS entries found"));
            return;
          }
          this.setRecords(origin, addresses);
          const records = this.#records.get(origin.hostname);
          const ip = this.pick(origin, records, newOpts.affinity);
          let port;
          if (typeof ip.port === "number") {
            port = `:${ip.port}`;
          } else if (origin.port !== "") {
            port = `:${origin.port}`;
          } else {
            port = "";
          }
          cb(null, `${origin.protocol}//${ip.family === 6 ? `[${ip.address}]` : ip.address}${port}`);
        });
      } else {
        const ip = this.pick(origin, ips, newOpts.affinity);
        if (ip == null) {
          this.#records.delete(origin.hostname);
          this.runLookup(origin, opts, cb);
          return;
        }
        let port;
        if (typeof ip.port === "number") {
          port = `:${ip.port}`;
        } else if (origin.port !== "") {
          port = `:${origin.port}`;
        } else {
          port = "";
        }
        cb(null, `${origin.protocol}//${ip.family === 6 ? `[${ip.address}]` : ip.address}${port}`);
      }
    }
    #defaultLookup(origin, opts, cb) {
      lookup(origin.hostname, {
        all: true,
        family: this.dualStack === false ? this.affinity : 0,
        order: "ipv4first"
      }, (err, addresses) => {
        if (err) {
          return cb(err);
        }
        const results = new Map;
        for (const addr of addresses) {
          results.set(`${addr.address}:${addr.family}`, addr);
        }
        cb(null, results.values());
      });
    }
    #defaultPick(origin, hostnameRecords, affinity) {
      let ip = null;
      const { records, offset } = hostnameRecords;
      let family;
      if (this.dualStack) {
        if (affinity == null) {
          if (offset == null || offset === maxInt) {
            hostnameRecords.offset = 0;
            affinity = 4;
          } else {
            hostnameRecords.offset++;
            affinity = (hostnameRecords.offset & 1) === 1 ? 6 : 4;
          }
        }
        if (records[affinity] != null && records[affinity].ips.length > 0) {
          family = records[affinity];
        } else {
          family = records[affinity === 4 ? 6 : 4];
        }
      } else {
        family = records[affinity];
      }
      if (family == null || family.ips.length === 0) {
        return ip;
      }
      if (family.offset == null || family.offset === maxInt) {
        family.offset = 0;
      } else {
        family.offset++;
      }
      const position = family.offset % family.ips.length;
      ip = family.ips[position] ?? null;
      if (ip == null) {
        return ip;
      }
      if (Date.now() - ip.timestamp > ip.ttl) {
        family.ips.splice(position, 1);
        return this.pick(origin, hostnameRecords, affinity);
      }
      return ip;
    }
    setRecords(origin, addresses) {
      const timestamp = Date.now();
      const records = { records: { 4: null, 6: null } };
      for (const record of addresses) {
        record.timestamp = timestamp;
        if (typeof record.ttl === "number") {
          record.ttl = Math.min(record.ttl, this.#maxTTL);
        } else {
          record.ttl = this.#maxTTL;
        }
        const familyRecords = records.records[record.family] ?? { ips: [] };
        familyRecords.ips.push(record);
        records.records[record.family] = familyRecords;
      }
      this.#records.set(origin.hostname, records);
    }
    getHandler(meta, opts) {
      return new DNSDispatchHandler(this, meta, opts);
    }
  }

  class DNSDispatchHandler extends DecoratorHandler {
    #state = null;
    #opts = null;
    #dispatch = null;
    #handler = null;
    #origin = null;
    constructor(state, { origin, handler, dispatch }, opts) {
      super(handler);
      this.#origin = origin;
      this.#handler = handler;
      this.#opts = { ...opts };
      this.#state = state;
      this.#dispatch = dispatch;
    }
    onError(err) {
      switch (err.code) {
        case "ETIMEDOUT":
        case "ECONNREFUSED": {
          if (this.#state.dualStack) {
            this.#state.runLookup(this.#origin, this.#opts, (err2, newOrigin) => {
              if (err2) {
                return this.#handler.onError(err2);
              }
              const dispatchOpts = {
                ...this.#opts,
                origin: newOrigin
              };
              this.#dispatch(dispatchOpts, this);
            });
            return;
          }
          this.#handler.onError(err);
          return;
        }
        case "ENOTFOUND":
          this.#state.deleteRecord(this.#origin);
        default:
          this.#handler.onError(err);
          break;
      }
    }
  }
  module.exports = (interceptorOpts) => {
    if (interceptorOpts?.maxTTL != null && (typeof interceptorOpts?.maxTTL !== "number" || interceptorOpts?.maxTTL < 0)) {
      throw new InvalidArgumentError("Invalid maxTTL. Must be a positive number");
    }
    if (interceptorOpts?.maxItems != null && (typeof interceptorOpts?.maxItems !== "number" || interceptorOpts?.maxItems < 1)) {
      throw new InvalidArgumentError("Invalid maxItems. Must be a positive number and greater than zero");
    }
    if (interceptorOpts?.affinity != null && interceptorOpts?.affinity !== 4 && interceptorOpts?.affinity !== 6) {
      throw new InvalidArgumentError("Invalid affinity. Must be either 4 or 6");
    }
    if (interceptorOpts?.dualStack != null && typeof interceptorOpts?.dualStack !== "boolean") {
      throw new InvalidArgumentError("Invalid dualStack. Must be a boolean");
    }
    if (interceptorOpts?.lookup != null && typeof interceptorOpts?.lookup !== "function") {
      throw new InvalidArgumentError("Invalid lookup. Must be a function");
    }
    if (interceptorOpts?.pick != null && typeof interceptorOpts?.pick !== "function") {
      throw new InvalidArgumentError("Invalid pick. Must be a function");
    }
    const dualStack = interceptorOpts?.dualStack ?? true;
    let affinity;
    if (dualStack) {
      affinity = interceptorOpts?.affinity ?? null;
    } else {
      affinity = interceptorOpts?.affinity ?? 4;
    }
    const opts = {
      maxTTL: interceptorOpts?.maxTTL ?? 1e4,
      lookup: interceptorOpts?.lookup ?? null,
      pick: interceptorOpts?.pick ?? null,
      dualStack,
      affinity,
      maxItems: interceptorOpts?.maxItems ?? Infinity
    };
    const instance = new DNSInstance(opts);
    return (dispatch) => {
      return function dnsInterceptor(origDispatchOpts, handler) {
        const origin = origDispatchOpts.origin.constructor === URL ? origDispatchOpts.origin : new URL(origDispatchOpts.origin);
        if (isIP(origin.hostname) !== 0) {
          return dispatch(origDispatchOpts, handler);
        }
        instance.runLookup(origin, origDispatchOpts, (err, newOrigin) => {
          if (err) {
            return handler.onError(err);
          }
          let dispatchOpts = null;
          dispatchOpts = {
            ...origDispatchOpts,
            servername: origin.hostname,
            origin: newOrigin,
            headers: {
              host: origin.hostname,
              ...origDispatchOpts.headers
            }
          };
          dispatch(dispatchOpts, instance.getHandler({ origin, dispatch, handler }, origDispatchOpts));
        });
        return true;
      };
    };
  };
});

// node_modules/undici/lib/web/fetch/headers.js
var require_headers = __commonJS((exports, module) => {
  var { kConstruct } = require_symbols();
  var { kEnumerableProperty } = require_util();
  var {
    iteratorMixin,
    isValidHeaderName,
    isValidHeaderValue
  } = require_util2();
  var { webidl } = require_webidl();
  var assert = __require("assert");
  var util = __require("util");
  var kHeadersMap = Symbol("headers map");
  var kHeadersSortedMap = Symbol("headers map sorted");
  function isHTTPWhiteSpaceCharCode(code) {
    return code === 10 || code === 13 || code === 9 || code === 32;
  }
  function headerValueNormalize(potentialValue) {
    let i = 0;
    let j = potentialValue.length;
    while (j > i && isHTTPWhiteSpaceCharCode(potentialValue.charCodeAt(j - 1)))
      --j;
    while (j > i && isHTTPWhiteSpaceCharCode(potentialValue.charCodeAt(i)))
      ++i;
    return i === 0 && j === potentialValue.length ? potentialValue : potentialValue.substring(i, j);
  }
  function fill(headers, object) {
    if (Array.isArray(object)) {
      for (let i = 0;i < object.length; ++i) {
        const header = object[i];
        if (header.length !== 2) {
          throw webidl.errors.exception({
            header: "Headers constructor",
            message: `expected name/value pair to be length 2, found ${header.length}.`
          });
        }
        appendHeader(headers, header[0], header[1]);
      }
    } else if (typeof object === "object" && object !== null) {
      const keys = Object.keys(object);
      for (let i = 0;i < keys.length; ++i) {
        appendHeader(headers, keys[i], object[keys[i]]);
      }
    } else {
      throw webidl.errors.conversionFailed({
        prefix: "Headers constructor",
        argument: "Argument 1",
        types: ["sequence<sequence<ByteString>>", "record<ByteString, ByteString>"]
      });
    }
  }
  function appendHeader(headers, name, value) {
    value = headerValueNormalize(value);
    if (!isValidHeaderName(name)) {
      throw webidl.errors.invalidArgument({
        prefix: "Headers.append",
        value: name,
        type: "header name"
      });
    } else if (!isValidHeaderValue(value)) {
      throw webidl.errors.invalidArgument({
        prefix: "Headers.append",
        value,
        type: "header value"
      });
    }
    if (getHeadersGuard(headers) === "immutable") {
      throw new TypeError("immutable");
    }
    return getHeadersList(headers).append(name, value, false);
  }
  function compareHeaderName(a, b) {
    return a[0] < b[0] ? -1 : 1;
  }

  class HeadersList {
    cookies = null;
    constructor(init) {
      if (init instanceof HeadersList) {
        this[kHeadersMap] = new Map(init[kHeadersMap]);
        this[kHeadersSortedMap] = init[kHeadersSortedMap];
        this.cookies = init.cookies === null ? null : [...init.cookies];
      } else {
        this[kHeadersMap] = new Map(init);
        this[kHeadersSortedMap] = null;
      }
    }
    contains(name, isLowerCase) {
      return this[kHeadersMap].has(isLowerCase ? name : name.toLowerCase());
    }
    clear() {
      this[kHeadersMap].clear();
      this[kHeadersSortedMap] = null;
      this.cookies = null;
    }
    append(name, value, isLowerCase) {
      this[kHeadersSortedMap] = null;
      const lowercaseName = isLowerCase ? name : name.toLowerCase();
      const exists = this[kHeadersMap].get(lowercaseName);
      if (exists) {
        const delimiter = lowercaseName === "cookie" ? "; " : ", ";
        this[kHeadersMap].set(lowercaseName, {
          name: exists.name,
          value: `${exists.value}${delimiter}${value}`
        });
      } else {
        this[kHeadersMap].set(lowercaseName, { name, value });
      }
      if (lowercaseName === "set-cookie") {
        (this.cookies ??= []).push(value);
      }
    }
    set(name, value, isLowerCase) {
      this[kHeadersSortedMap] = null;
      const lowercaseName = isLowerCase ? name : name.toLowerCase();
      if (lowercaseName === "set-cookie") {
        this.cookies = [value];
      }
      this[kHeadersMap].set(lowercaseName, { name, value });
    }
    delete(name, isLowerCase) {
      this[kHeadersSortedMap] = null;
      if (!isLowerCase)
        name = name.toLowerCase();
      if (name === "set-cookie") {
        this.cookies = null;
      }
      this[kHeadersMap].delete(name);
    }
    get(name, isLowerCase) {
      return this[kHeadersMap].get(isLowerCase ? name : name.toLowerCase())?.value ?? null;
    }
    *[Symbol.iterator]() {
      for (const { 0: name, 1: { value } } of this[kHeadersMap]) {
        yield [name, value];
      }
    }
    get entries() {
      const headers = {};
      if (this[kHeadersMap].size !== 0) {
        for (const { name, value } of this[kHeadersMap].values()) {
          headers[name] = value;
        }
      }
      return headers;
    }
    rawValues() {
      return this[kHeadersMap].values();
    }
    get entriesList() {
      const headers = [];
      if (this[kHeadersMap].size !== 0) {
        for (const { 0: lowerName, 1: { name, value } } of this[kHeadersMap]) {
          if (lowerName === "set-cookie") {
            for (const cookie of this.cookies) {
              headers.push([name, cookie]);
            }
          } else {
            headers.push([name, value]);
          }
        }
      }
      return headers;
    }
    toSortedArray() {
      const size = this[kHeadersMap].size;
      const array = new Array(size);
      if (size <= 32) {
        if (size === 0) {
          return array;
        }
        const iterator = this[kHeadersMap][Symbol.iterator]();
        const firstValue = iterator.next().value;
        array[0] = [firstValue[0], firstValue[1].value];
        assert(firstValue[1].value !== null);
        for (let i = 1, j = 0, right = 0, left = 0, pivot = 0, x, value;i < size; ++i) {
          value = iterator.next().value;
          x = array[i] = [value[0], value[1].value];
          assert(x[1] !== null);
          left = 0;
          right = i;
          while (left < right) {
            pivot = left + (right - left >> 1);
            if (array[pivot][0] <= x[0]) {
              left = pivot + 1;
            } else {
              right = pivot;
            }
          }
          if (i !== pivot) {
            j = i;
            while (j > left) {
              array[j] = array[--j];
            }
            array[left] = x;
          }
        }
        if (!iterator.next().done) {
          throw new TypeError("Unreachable");
        }
        return array;
      } else {
        let i = 0;
        for (const { 0: name, 1: { value } } of this[kHeadersMap]) {
          array[i++] = [name, value];
          assert(value !== null);
        }
        return array.sort(compareHeaderName);
      }
    }
  }

  class Headers2 {
    #guard;
    #headersList;
    constructor(init = undefined) {
      webidl.util.markAsUncloneable(this);
      if (init === kConstruct) {
        return;
      }
      this.#headersList = new HeadersList;
      this.#guard = "none";
      if (init !== undefined) {
        init = webidl.converters.HeadersInit(init, "Headers contructor", "init");
        fill(this, init);
      }
    }
    append(name, value) {
      webidl.brandCheck(this, Headers2);
      webidl.argumentLengthCheck(arguments, 2, "Headers.append");
      const prefix = "Headers.append";
      name = webidl.converters.ByteString(name, prefix, "name");
      value = webidl.converters.ByteString(value, prefix, "value");
      return appendHeader(this, name, value);
    }
    delete(name) {
      webidl.brandCheck(this, Headers2);
      webidl.argumentLengthCheck(arguments, 1, "Headers.delete");
      const prefix = "Headers.delete";
      name = webidl.converters.ByteString(name, prefix, "name");
      if (!isValidHeaderName(name)) {
        throw webidl.errors.invalidArgument({
          prefix: "Headers.delete",
          value: name,
          type: "header name"
        });
      }
      if (this.#guard === "immutable") {
        throw new TypeError("immutable");
      }
      if (!this.#headersList.contains(name, false)) {
        return;
      }
      this.#headersList.delete(name, false);
    }
    get(name) {
      webidl.brandCheck(this, Headers2);
      webidl.argumentLengthCheck(arguments, 1, "Headers.get");
      const prefix = "Headers.get";
      name = webidl.converters.ByteString(name, prefix, "name");
      if (!isValidHeaderName(name)) {
        throw webidl.errors.invalidArgument({
          prefix,
          value: name,
          type: "header name"
        });
      }
      return this.#headersList.get(name, false);
    }
    has(name) {
      webidl.brandCheck(this, Headers2);
      webidl.argumentLengthCheck(arguments, 1, "Headers.has");
      const prefix = "Headers.has";
      name = webidl.converters.ByteString(name, prefix, "name");
      if (!isValidHeaderName(name)) {
        throw webidl.errors.invalidArgument({
          prefix,
          value: name,
          type: "header name"
        });
      }
      return this.#headersList.contains(name, false);
    }
    set(name, value) {
      webidl.brandCheck(this, Headers2);
      webidl.argumentLengthCheck(arguments, 2, "Headers.set");
      const prefix = "Headers.set";
      name = webidl.converters.ByteString(name, prefix, "name");
      value = webidl.converters.ByteString(value, prefix, "value");
      value = headerValueNormalize(value);
      if (!isValidHeaderName(name)) {
        throw webidl.errors.invalidArgument({
          prefix,
          value: name,
          type: "header name"
        });
      } else if (!isValidHeaderValue(value)) {
        throw webidl.errors.invalidArgument({
          prefix,
          value,
          type: "header value"
        });
      }
      if (this.#guard === "immutable") {
        throw new TypeError("immutable");
      }
      this.#headersList.set(name, value, false);
    }
    getSetCookie() {
      webidl.brandCheck(this, Headers2);
      const list = this.#headersList.cookies;
      if (list) {
        return [...list];
      }
      return [];
    }
    get [kHeadersSortedMap]() {
      if (this.#headersList[kHeadersSortedMap]) {
        return this.#headersList[kHeadersSortedMap];
      }
      const headers = [];
      const names = this.#headersList.toSortedArray();
      const cookies = this.#headersList.cookies;
      if (cookies === null || cookies.length === 1) {
        return this.#headersList[kHeadersSortedMap] = names;
      }
      for (let i = 0;i < names.length; ++i) {
        const { 0: name, 1: value } = names[i];
        if (name === "set-cookie") {
          for (let j = 0;j < cookies.length; ++j) {
            headers.push([name, cookies[j]]);
          }
        } else {
          headers.push([name, value]);
        }
      }
      return this.#headersList[kHeadersSortedMap] = headers;
    }
    [util.inspect.custom](depth, options) {
      options.depth ??= depth;
      return `Headers ${util.formatWithOptions(options, this.#headersList.entries)}`;
    }
    static getHeadersGuard(o) {
      return o.#guard;
    }
    static setHeadersGuard(o, guard) {
      o.#guard = guard;
    }
    static getHeadersList(o) {
      return o.#headersList;
    }
    static setHeadersList(o, list) {
      o.#headersList = list;
    }
  }
  var { getHeadersGuard, setHeadersGuard, getHeadersList, setHeadersList } = Headers2;
  Reflect.deleteProperty(Headers2, "getHeadersGuard");
  Reflect.deleteProperty(Headers2, "setHeadersGuard");
  Reflect.deleteProperty(Headers2, "getHeadersList");
  Reflect.deleteProperty(Headers2, "setHeadersList");
  iteratorMixin("Headers", Headers2, kHeadersSortedMap, 0, 1);
  Object.defineProperties(Headers2.prototype, {
    append: kEnumerableProperty,
    delete: kEnumerableProperty,
    get: kEnumerableProperty,
    has: kEnumerableProperty,
    set: kEnumerableProperty,
    getSetCookie: kEnumerableProperty,
    [Symbol.toStringTag]: {
      value: "Headers",
      configurable: true
    },
    [util.inspect.custom]: {
      enumerable: false
    }
  });
  webidl.converters.HeadersInit = function(V, prefix, argument) {
    if (webidl.util.Type(V) === "Object") {
      const iterator = Reflect.get(V, Symbol.iterator);
      if (!util.types.isProxy(V) && iterator === Headers2.prototype.entries) {
        try {
          return getHeadersList(V).entriesList;
        } catch {}
      }
      if (typeof iterator === "function") {
        return webidl.converters["sequence<sequence<ByteString>>"](V, prefix, argument, iterator.bind(V));
      }
      return webidl.converters["record<ByteString, ByteString>"](V, prefix, argument);
    }
    throw webidl.errors.conversionFailed({
      prefix: "Headers constructor",
      argument: "Argument 1",
      types: ["sequence<sequence<ByteString>>", "record<ByteString, ByteString>"]
    });
  };
  module.exports = {
    fill,
    compareHeaderName,
    Headers: Headers2,
    HeadersList,
    getHeadersGuard,
    setHeadersGuard,
    setHeadersList,
    getHeadersList
  };
});

// node_modules/undici/lib/web/fetch/response.js
var require_response = __commonJS((exports, module) => {
  var { Headers: Headers2, HeadersList, fill, getHeadersGuard, setHeadersGuard, setHeadersList } = require_headers();
  var { extractBody, cloneBody, mixinBody, hasFinalizationRegistry, streamRegistry, bodyUnusable } = require_body();
  var util = require_util();
  var nodeUtil = __require("util");
  var { kEnumerableProperty } = util;
  var {
    isValidReasonPhrase,
    isCancelled,
    isAborted,
    isBlobLike,
    serializeJavascriptValueToJSONString,
    isErrorLike,
    isomorphicEncode,
    environmentSettingsObject: relevantRealm
  } = require_util2();
  var {
    redirectStatusSet,
    nullBodyStatus
  } = require_constants3();
  var { kState, kHeaders } = require_symbols2();
  var { webidl } = require_webidl();
  var { FormData } = require_formdata();
  var { URLSerializer } = require_data_url();
  var { kConstruct } = require_symbols();
  var assert = __require("assert");
  var { types } = __require("util");
  var textEncoder = new TextEncoder("utf-8");

  class Response2 {
    static error() {
      const responseObject = fromInnerResponse(makeNetworkError(), "immutable");
      return responseObject;
    }
    static json(data, init = {}) {
      webidl.argumentLengthCheck(arguments, 1, "Response.json");
      if (init !== null) {
        init = webidl.converters.ResponseInit(init);
      }
      const bytes = textEncoder.encode(serializeJavascriptValueToJSONString(data));
      const body = extractBody(bytes);
      const responseObject = fromInnerResponse(makeResponse({}), "response");
      initializeResponse(responseObject, init, { body: body[0], type: "application/json" });
      return responseObject;
    }
    static redirect(url, status = 302) {
      webidl.argumentLengthCheck(arguments, 1, "Response.redirect");
      url = webidl.converters.USVString(url);
      status = webidl.converters["unsigned short"](status);
      let parsedURL;
      try {
        parsedURL = new URL(url, relevantRealm.settingsObject.baseUrl);
      } catch (err) {
        throw new TypeError(`Failed to parse URL from ${url}`, { cause: err });
      }
      if (!redirectStatusSet.has(status)) {
        throw new RangeError(`Invalid status code ${status}`);
      }
      const responseObject = fromInnerResponse(makeResponse({}), "immutable");
      responseObject[kState].status = status;
      const value = isomorphicEncode(URLSerializer(parsedURL));
      responseObject[kState].headersList.append("location", value, true);
      return responseObject;
    }
    constructor(body = null, init = {}) {
      webidl.util.markAsUncloneable(this);
      if (body === kConstruct) {
        return;
      }
      if (body !== null) {
        body = webidl.converters.BodyInit(body);
      }
      init = webidl.converters.ResponseInit(init);
      this[kState] = makeResponse({});
      this[kHeaders] = new Headers2(kConstruct);
      setHeadersGuard(this[kHeaders], "response");
      setHeadersList(this[kHeaders], this[kState].headersList);
      let bodyWithType = null;
      if (body != null) {
        const [extractedBody, type] = extractBody(body);
        bodyWithType = { body: extractedBody, type };
      }
      initializeResponse(this, init, bodyWithType);
    }
    get type() {
      webidl.brandCheck(this, Response2);
      return this[kState].type;
    }
    get url() {
      webidl.brandCheck(this, Response2);
      const urlList = this[kState].urlList;
      const url = urlList[urlList.length - 1] ?? null;
      if (url === null) {
        return "";
      }
      return URLSerializer(url, true);
    }
    get redirected() {
      webidl.brandCheck(this, Response2);
      return this[kState].urlList.length > 1;
    }
    get status() {
      webidl.brandCheck(this, Response2);
      return this[kState].status;
    }
    get ok() {
      webidl.brandCheck(this, Response2);
      return this[kState].status >= 200 && this[kState].status <= 299;
    }
    get statusText() {
      webidl.brandCheck(this, Response2);
      return this[kState].statusText;
    }
    get headers() {
      webidl.brandCheck(this, Response2);
      return this[kHeaders];
    }
    get body() {
      webidl.brandCheck(this, Response2);
      return this[kState].body ? this[kState].body.stream : null;
    }
    get bodyUsed() {
      webidl.brandCheck(this, Response2);
      return !!this[kState].body && util.isDisturbed(this[kState].body.stream);
    }
    clone() {
      webidl.brandCheck(this, Response2);
      if (bodyUnusable(this)) {
        throw webidl.errors.exception({
          header: "Response.clone",
          message: "Body has already been consumed."
        });
      }
      const clonedResponse = cloneResponse(this[kState]);
      if (hasFinalizationRegistry && this[kState].body?.stream) {
        streamRegistry.register(this, new WeakRef(this[kState].body.stream));
      }
      return fromInnerResponse(clonedResponse, getHeadersGuard(this[kHeaders]));
    }
    [nodeUtil.inspect.custom](depth, options) {
      if (options.depth === null) {
        options.depth = 2;
      }
      options.colors ??= true;
      const properties = {
        status: this.status,
        statusText: this.statusText,
        headers: this.headers,
        body: this.body,
        bodyUsed: this.bodyUsed,
        ok: this.ok,
        redirected: this.redirected,
        type: this.type,
        url: this.url
      };
      return `Response ${nodeUtil.formatWithOptions(options, properties)}`;
    }
  }
  mixinBody(Response2);
  Object.defineProperties(Response2.prototype, {
    type: kEnumerableProperty,
    url: kEnumerableProperty,
    status: kEnumerableProperty,
    ok: kEnumerableProperty,
    redirected: kEnumerableProperty,
    statusText: kEnumerableProperty,
    headers: kEnumerableProperty,
    clone: kEnumerableProperty,
    body: kEnumerableProperty,
    bodyUsed: kEnumerableProperty,
    [Symbol.toStringTag]: {
      value: "Response",
      configurable: true
    }
  });
  Object.defineProperties(Response2, {
    json: kEnumerableProperty,
    redirect: kEnumerableProperty,
    error: kEnumerableProperty
  });
  function cloneResponse(response) {
    if (response.internalResponse) {
      return filterResponse(cloneResponse(response.internalResponse), response.type);
    }
    const newResponse = makeResponse({ ...response, body: null });
    if (response.body != null) {
      newResponse.body = cloneBody(newResponse, response.body);
    }
    return newResponse;
  }
  function makeResponse(init) {
    return {
      aborted: false,
      rangeRequested: false,
      timingAllowPassed: false,
      requestIncludesCredentials: false,
      type: "default",
      status: 200,
      timingInfo: null,
      cacheState: "",
      statusText: "",
      ...init,
      headersList: init?.headersList ? new HeadersList(init?.headersList) : new HeadersList,
      urlList: init?.urlList ? [...init.urlList] : []
    };
  }
  function makeNetworkError(reason) {
    const isError = isErrorLike(reason);
    return makeResponse({
      type: "error",
      status: 0,
      error: isError ? reason : new Error(reason ? String(reason) : reason),
      aborted: reason && reason.name === "AbortError"
    });
  }
  function isNetworkError(response) {
    return response.type === "error" && response.status === 0;
  }
  function makeFilteredResponse(response, state) {
    state = {
      internalResponse: response,
      ...state
    };
    return new Proxy(response, {
      get(target, p) {
        return p in state ? state[p] : target[p];
      },
      set(target, p, value) {
        assert(!(p in state));
        target[p] = value;
        return true;
      }
    });
  }
  function filterResponse(response, type) {
    if (type === "basic") {
      return makeFilteredResponse(response, {
        type: "basic",
        headersList: response.headersList
      });
    } else if (type === "cors") {
      return makeFilteredResponse(response, {
        type: "cors",
        headersList: response.headersList
      });
    } else if (type === "opaque") {
      return makeFilteredResponse(response, {
        type: "opaque",
        urlList: Object.freeze([]),
        status: 0,
        statusText: "",
        body: null
      });
    } else if (type === "opaqueredirect") {
      return makeFilteredResponse(response, {
        type: "opaqueredirect",
        status: 0,
        statusText: "",
        headersList: [],
        body: null
      });
    } else {
      assert(false);
    }
  }
  function makeAppropriateNetworkError(fetchParams, err = null) {
    assert(isCancelled(fetchParams));
    return isAborted(fetchParams) ? makeNetworkError(Object.assign(new DOMException("The operation was aborted.", "AbortError"), { cause: err })) : makeNetworkError(Object.assign(new DOMException("Request was cancelled."), { cause: err }));
  }
  function initializeResponse(response, init, body) {
    if (init.status !== null && (init.status < 200 || init.status > 599)) {
      throw new RangeError('init["status"] must be in the range of 200 to 599, inclusive.');
    }
    if ("statusText" in init && init.statusText != null) {
      if (!isValidReasonPhrase(String(init.statusText))) {
        throw new TypeError("Invalid statusText");
      }
    }
    if ("status" in init && init.status != null) {
      response[kState].status = init.status;
    }
    if ("statusText" in init && init.statusText != null) {
      response[kState].statusText = init.statusText;
    }
    if ("headers" in init && init.headers != null) {
      fill(response[kHeaders], init.headers);
    }
    if (body) {
      if (nullBodyStatus.includes(response.status)) {
        throw webidl.errors.exception({
          header: "Response constructor",
          message: `Invalid response status code ${response.status}`
        });
      }
      response[kState].body = body.body;
      if (body.type != null && !response[kState].headersList.contains("content-type", true)) {
        response[kState].headersList.append("content-type", body.type, true);
      }
    }
  }
  function fromInnerResponse(innerResponse, guard) {
    const response = new Response2(kConstruct);
    response[kState] = innerResponse;
    response[kHeaders] = new Headers2(kConstruct);
    setHeadersList(response[kHeaders], innerResponse.headersList);
    setHeadersGuard(response[kHeaders], guard);
    if (hasFinalizationRegistry && innerResponse.body?.stream) {
      streamRegistry.register(response, new WeakRef(innerResponse.body.stream));
    }
    return response;
  }
  webidl.converters.ReadableStream = webidl.interfaceConverter(ReadableStream);
  webidl.converters.FormData = webidl.interfaceConverter(FormData);
  webidl.converters.URLSearchParams = webidl.interfaceConverter(URLSearchParams);
  webidl.converters.XMLHttpRequestBodyInit = function(V, prefix, name) {
    if (typeof V === "string") {
      return webidl.converters.USVString(V, prefix, name);
    }
    if (isBlobLike(V)) {
      return webidl.converters.Blob(V, prefix, name, { strict: false });
    }
    if (ArrayBuffer.isView(V) || types.isArrayBuffer(V)) {
      return webidl.converters.BufferSource(V, prefix, name);
    }
    if (util.isFormDataLike(V)) {
      return webidl.converters.FormData(V, prefix, name, { strict: false });
    }
    if (V instanceof URLSearchParams) {
      return webidl.converters.URLSearchParams(V, prefix, name);
    }
    return webidl.converters.DOMString(V, prefix, name);
  };
  webidl.converters.BodyInit = function(V, prefix, argument) {
    if (V instanceof ReadableStream) {
      return webidl.converters.ReadableStream(V, prefix, argument);
    }
    if (V?.[Symbol.asyncIterator]) {
      return V;
    }
    return webidl.converters.XMLHttpRequestBodyInit(V, prefix, argument);
  };
  webidl.converters.ResponseInit = webidl.dictionaryConverter([
    {
      key: "status",
      converter: webidl.converters["unsigned short"],
      defaultValue: () => 200
    },
    {
      key: "statusText",
      converter: webidl.converters.ByteString,
      defaultValue: () => ""
    },
    {
      key: "headers",
      converter: webidl.converters.HeadersInit
    }
  ]);
  module.exports = {
    isNetworkError,
    makeNetworkError,
    makeResponse,
    makeAppropriateNetworkError,
    filterResponse,
    Response: Response2,
    cloneResponse,
    fromInnerResponse
  };
});

// node_modules/undici/lib/web/fetch/dispatcher-weakref.js
var require_dispatcher_weakref = __commonJS((exports, module) => {
  var { kConnected, kSize } = require_symbols();

  class CompatWeakRef {
    constructor(value) {
      this.value = value;
    }
    deref() {
      return this.value[kConnected] === 0 && this.value[kSize] === 0 ? undefined : this.value;
    }
  }

  class CompatFinalizer {
    constructor(finalizer) {
      this.finalizer = finalizer;
    }
    register(dispatcher, key) {
      if (dispatcher.on) {
        dispatcher.on("disconnect", () => {
          if (dispatcher[kConnected] === 0 && dispatcher[kSize] === 0) {
            this.finalizer(key);
          }
        });
      }
    }
    unregister(key) {}
  }
  module.exports = function() {
    if (process.env.NODE_V8_COVERAGE && process.version.startsWith("v18")) {
      process._rawDebug("Using compatibility WeakRef and FinalizationRegistry");
      return {
        WeakRef: CompatWeakRef,
        FinalizationRegistry: CompatFinalizer
      };
    }
    return { WeakRef, FinalizationRegistry };
  };
});

// node_modules/undici/lib/web/fetch/request.js
var require_request2 = __commonJS((exports, module) => {
  var { extractBody, mixinBody, cloneBody, bodyUnusable } = require_body();
  var { Headers: Headers2, fill: fillHeaders, HeadersList, setHeadersGuard, getHeadersGuard, setHeadersList, getHeadersList } = require_headers();
  var { FinalizationRegistry: FinalizationRegistry2 } = require_dispatcher_weakref()();
  var util = require_util();
  var nodeUtil = __require("util");
  var {
    isValidHTTPToken,
    sameOrigin,
    environmentSettingsObject
  } = require_util2();
  var {
    forbiddenMethodsSet,
    corsSafeListedMethodsSet,
    referrerPolicy,
    requestRedirect,
    requestMode,
    requestCredentials,
    requestCache,
    requestDuplex
  } = require_constants3();
  var { kEnumerableProperty, normalizedMethodRecordsBase, normalizedMethodRecords } = util;
  var { kHeaders, kSignal, kState, kDispatcher } = require_symbols2();
  var { webidl } = require_webidl();
  var { URLSerializer } = require_data_url();
  var { kConstruct } = require_symbols();
  var assert = __require("assert");
  var { getMaxListeners, setMaxListeners, getEventListeners, defaultMaxListeners } = __require("events");
  var kAbortController = Symbol("abortController");
  var requestFinalizer = new FinalizationRegistry2(({ signal, abort }) => {
    signal.removeEventListener("abort", abort);
  });
  var dependentControllerMap = new WeakMap;
  function buildAbort(acRef) {
    return abort;
    function abort() {
      const ac = acRef.deref();
      if (ac !== undefined) {
        requestFinalizer.unregister(abort);
        this.removeEventListener("abort", abort);
        ac.abort(this.reason);
        const controllerList = dependentControllerMap.get(ac.signal);
        if (controllerList !== undefined) {
          if (controllerList.size !== 0) {
            for (const ref of controllerList) {
              const ctrl = ref.deref();
              if (ctrl !== undefined) {
                ctrl.abort(this.reason);
              }
            }
            controllerList.clear();
          }
          dependentControllerMap.delete(ac.signal);
        }
      }
    }
  }
  var patchMethodWarning = false;

  class Request2 {
    constructor(input, init = {}) {
      webidl.util.markAsUncloneable(this);
      if (input === kConstruct) {
        return;
      }
      const prefix = "Request constructor";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      input = webidl.converters.RequestInfo(input, prefix, "input");
      init = webidl.converters.RequestInit(init, prefix, "init");
      let request = null;
      let fallbackMode = null;
      const baseUrl = environmentSettingsObject.settingsObject.baseUrl;
      let signal = null;
      if (typeof input === "string") {
        this[kDispatcher] = init.dispatcher;
        let parsedURL;
        try {
          parsedURL = new URL(input, baseUrl);
        } catch (err) {
          throw new TypeError("Failed to parse URL from " + input, { cause: err });
        }
        if (parsedURL.username || parsedURL.password) {
          throw new TypeError("Request cannot be constructed from a URL that includes credentials: " + input);
        }
        request = makeRequest({ urlList: [parsedURL] });
        fallbackMode = "cors";
      } else {
        this[kDispatcher] = init.dispatcher || input[kDispatcher];
        assert(input instanceof Request2);
        request = input[kState];
        signal = input[kSignal];
      }
      const origin = environmentSettingsObject.settingsObject.origin;
      let window = "client";
      if (request.window?.constructor?.name === "EnvironmentSettingsObject" && sameOrigin(request.window, origin)) {
        window = request.window;
      }
      if (init.window != null) {
        throw new TypeError(`'window' option '${window}' must be null`);
      }
      if ("window" in init) {
        window = "no-window";
      }
      request = makeRequest({
        method: request.method,
        headersList: request.headersList,
        unsafeRequest: request.unsafeRequest,
        client: environmentSettingsObject.settingsObject,
        window,
        priority: request.priority,
        origin: request.origin,
        referrer: request.referrer,
        referrerPolicy: request.referrerPolicy,
        mode: request.mode,
        credentials: request.credentials,
        cache: request.cache,
        redirect: request.redirect,
        integrity: request.integrity,
        keepalive: request.keepalive,
        reloadNavigation: request.reloadNavigation,
        historyNavigation: request.historyNavigation,
        urlList: [...request.urlList]
      });
      const initHasKey = Object.keys(init).length !== 0;
      if (initHasKey) {
        if (request.mode === "navigate") {
          request.mode = "same-origin";
        }
        request.reloadNavigation = false;
        request.historyNavigation = false;
        request.origin = "client";
        request.referrer = "client";
        request.referrerPolicy = "";
        request.url = request.urlList[request.urlList.length - 1];
        request.urlList = [request.url];
      }
      if (init.referrer !== undefined) {
        const referrer = init.referrer;
        if (referrer === "") {
          request.referrer = "no-referrer";
        } else {
          let parsedReferrer;
          try {
            parsedReferrer = new URL(referrer, baseUrl);
          } catch (err) {
            throw new TypeError(`Referrer "${referrer}" is not a valid URL.`, { cause: err });
          }
          if (parsedReferrer.protocol === "about:" && parsedReferrer.hostname === "client" || origin && !sameOrigin(parsedReferrer, environmentSettingsObject.settingsObject.baseUrl)) {
            request.referrer = "client";
          } else {
            request.referrer = parsedReferrer;
          }
        }
      }
      if (init.referrerPolicy !== undefined) {
        request.referrerPolicy = init.referrerPolicy;
      }
      let mode;
      if (init.mode !== undefined) {
        mode = init.mode;
      } else {
        mode = fallbackMode;
      }
      if (mode === "navigate") {
        throw webidl.errors.exception({
          header: "Request constructor",
          message: "invalid request mode navigate."
        });
      }
      if (mode != null) {
        request.mode = mode;
      }
      if (init.credentials !== undefined) {
        request.credentials = init.credentials;
      }
      if (init.cache !== undefined) {
        request.cache = init.cache;
      }
      if (request.cache === "only-if-cached" && request.mode !== "same-origin") {
        throw new TypeError("'only-if-cached' can be set only with 'same-origin' mode");
      }
      if (init.redirect !== undefined) {
        request.redirect = init.redirect;
      }
      if (init.integrity != null) {
        request.integrity = String(init.integrity);
      }
      if (init.keepalive !== undefined) {
        request.keepalive = Boolean(init.keepalive);
      }
      if (init.method !== undefined) {
        let method = init.method;
        const mayBeNormalized = normalizedMethodRecords[method];
        if (mayBeNormalized !== undefined) {
          request.method = mayBeNormalized;
        } else {
          if (!isValidHTTPToken(method)) {
            throw new TypeError(`'${method}' is not a valid HTTP method.`);
          }
          const upperCase = method.toUpperCase();
          if (forbiddenMethodsSet.has(upperCase)) {
            throw new TypeError(`'${method}' HTTP method is unsupported.`);
          }
          method = normalizedMethodRecordsBase[upperCase] ?? method;
          request.method = method;
        }
        if (!patchMethodWarning && request.method === "patch") {
          process.emitWarning("Using `patch` is highly likely to result in a `405 Method Not Allowed`. `PATCH` is much more likely to succeed.", {
            code: "UNDICI-FETCH-patch"
          });
          patchMethodWarning = true;
        }
      }
      if (init.signal !== undefined) {
        signal = init.signal;
      }
      this[kState] = request;
      const ac = new AbortController;
      this[kSignal] = ac.signal;
      if (signal != null) {
        if (!signal || typeof signal.aborted !== "boolean" || typeof signal.addEventListener !== "function") {
          throw new TypeError("Failed to construct 'Request': member signal is not of type AbortSignal.");
        }
        if (signal.aborted) {
          ac.abort(signal.reason);
        } else {
          this[kAbortController] = ac;
          const acRef = new WeakRef(ac);
          const abort = buildAbort(acRef);
          try {
            if (typeof getMaxListeners === "function" && getMaxListeners(signal) === defaultMaxListeners) {
              setMaxListeners(1500, signal);
            } else if (getEventListeners(signal, "abort").length >= defaultMaxListeners) {
              setMaxListeners(1500, signal);
            }
          } catch {}
          util.addAbortListener(signal, abort);
          requestFinalizer.register(ac, { signal, abort }, abort);
        }
      }
      this[kHeaders] = new Headers2(kConstruct);
      setHeadersList(this[kHeaders], request.headersList);
      setHeadersGuard(this[kHeaders], "request");
      if (mode === "no-cors") {
        if (!corsSafeListedMethodsSet.has(request.method)) {
          throw new TypeError(`'${request.method} is unsupported in no-cors mode.`);
        }
        setHeadersGuard(this[kHeaders], "request-no-cors");
      }
      if (initHasKey) {
        const headersList = getHeadersList(this[kHeaders]);
        const headers = init.headers !== undefined ? init.headers : new HeadersList(headersList);
        headersList.clear();
        if (headers instanceof HeadersList) {
          for (const { name, value } of headers.rawValues()) {
            headersList.append(name, value, false);
          }
          headersList.cookies = headers.cookies;
        } else {
          fillHeaders(this[kHeaders], headers);
        }
      }
      const inputBody = input instanceof Request2 ? input[kState].body : null;
      if ((init.body != null || inputBody != null) && (request.method === "GET" || request.method === "HEAD")) {
        throw new TypeError("Request with GET/HEAD method cannot have body.");
      }
      let initBody = null;
      if (init.body != null) {
        const [extractedBody, contentType] = extractBody(init.body, request.keepalive);
        initBody = extractedBody;
        if (contentType && !getHeadersList(this[kHeaders]).contains("content-type", true)) {
          this[kHeaders].append("content-type", contentType);
        }
      }
      const inputOrInitBody = initBody ?? inputBody;
      if (inputOrInitBody != null && inputOrInitBody.source == null) {
        if (initBody != null && init.duplex == null) {
          throw new TypeError("RequestInit: duplex option is required when sending a body.");
        }
        if (request.mode !== "same-origin" && request.mode !== "cors") {
          throw new TypeError('If request is made from ReadableStream, mode should be "same-origin" or "cors"');
        }
        request.useCORSPreflightFlag = true;
      }
      let finalBody = inputOrInitBody;
      if (initBody == null && inputBody != null) {
        if (bodyUnusable(input)) {
          throw new TypeError("Cannot construct a Request with a Request object that has already been used.");
        }
        const identityTransform = new TransformStream;
        inputBody.stream.pipeThrough(identityTransform);
        finalBody = {
          source: inputBody.source,
          length: inputBody.length,
          stream: identityTransform.readable
        };
      }
      this[kState].body = finalBody;
    }
    get method() {
      webidl.brandCheck(this, Request2);
      return this[kState].method;
    }
    get url() {
      webidl.brandCheck(this, Request2);
      return URLSerializer(this[kState].url);
    }
    get headers() {
      webidl.brandCheck(this, Request2);
      return this[kHeaders];
    }
    get destination() {
      webidl.brandCheck(this, Request2);
      return this[kState].destination;
    }
    get referrer() {
      webidl.brandCheck(this, Request2);
      if (this[kState].referrer === "no-referrer") {
        return "";
      }
      if (this[kState].referrer === "client") {
        return "about:client";
      }
      return this[kState].referrer.toString();
    }
    get referrerPolicy() {
      webidl.brandCheck(this, Request2);
      return this[kState].referrerPolicy;
    }
    get mode() {
      webidl.brandCheck(this, Request2);
      return this[kState].mode;
    }
    get credentials() {
      return this[kState].credentials;
    }
    get cache() {
      webidl.brandCheck(this, Request2);
      return this[kState].cache;
    }
    get redirect() {
      webidl.brandCheck(this, Request2);
      return this[kState].redirect;
    }
    get integrity() {
      webidl.brandCheck(this, Request2);
      return this[kState].integrity;
    }
    get keepalive() {
      webidl.brandCheck(this, Request2);
      return this[kState].keepalive;
    }
    get isReloadNavigation() {
      webidl.brandCheck(this, Request2);
      return this[kState].reloadNavigation;
    }
    get isHistoryNavigation() {
      webidl.brandCheck(this, Request2);
      return this[kState].historyNavigation;
    }
    get signal() {
      webidl.brandCheck(this, Request2);
      return this[kSignal];
    }
    get body() {
      webidl.brandCheck(this, Request2);
      return this[kState].body ? this[kState].body.stream : null;
    }
    get bodyUsed() {
      webidl.brandCheck(this, Request2);
      return !!this[kState].body && util.isDisturbed(this[kState].body.stream);
    }
    get duplex() {
      webidl.brandCheck(this, Request2);
      return "half";
    }
    clone() {
      webidl.brandCheck(this, Request2);
      if (bodyUnusable(this)) {
        throw new TypeError("unusable");
      }
      const clonedRequest = cloneRequest(this[kState]);
      const ac = new AbortController;
      if (this.signal.aborted) {
        ac.abort(this.signal.reason);
      } else {
        let list = dependentControllerMap.get(this.signal);
        if (list === undefined) {
          list = new Set;
          dependentControllerMap.set(this.signal, list);
        }
        const acRef = new WeakRef(ac);
        list.add(acRef);
        util.addAbortListener(ac.signal, buildAbort(acRef));
      }
      return fromInnerRequest(clonedRequest, ac.signal, getHeadersGuard(this[kHeaders]));
    }
    [nodeUtil.inspect.custom](depth, options) {
      if (options.depth === null) {
        options.depth = 2;
      }
      options.colors ??= true;
      const properties = {
        method: this.method,
        url: this.url,
        headers: this.headers,
        destination: this.destination,
        referrer: this.referrer,
        referrerPolicy: this.referrerPolicy,
        mode: this.mode,
        credentials: this.credentials,
        cache: this.cache,
        redirect: this.redirect,
        integrity: this.integrity,
        keepalive: this.keepalive,
        isReloadNavigation: this.isReloadNavigation,
        isHistoryNavigation: this.isHistoryNavigation,
        signal: this.signal
      };
      return `Request ${nodeUtil.formatWithOptions(options, properties)}`;
    }
  }
  mixinBody(Request2);
  function makeRequest(init) {
    return {
      method: init.method ?? "GET",
      localURLsOnly: init.localURLsOnly ?? false,
      unsafeRequest: init.unsafeRequest ?? false,
      body: init.body ?? null,
      client: init.client ?? null,
      reservedClient: init.reservedClient ?? null,
      replacesClientId: init.replacesClientId ?? "",
      window: init.window ?? "client",
      keepalive: init.keepalive ?? false,
      serviceWorkers: init.serviceWorkers ?? "all",
      initiator: init.initiator ?? "",
      destination: init.destination ?? "",
      priority: init.priority ?? null,
      origin: init.origin ?? "client",
      policyContainer: init.policyContainer ?? "client",
      referrer: init.referrer ?? "client",
      referrerPolicy: init.referrerPolicy ?? "",
      mode: init.mode ?? "no-cors",
      useCORSPreflightFlag: init.useCORSPreflightFlag ?? false,
      credentials: init.credentials ?? "same-origin",
      useCredentials: init.useCredentials ?? false,
      cache: init.cache ?? "default",
      redirect: init.redirect ?? "follow",
      integrity: init.integrity ?? "",
      cryptoGraphicsNonceMetadata: init.cryptoGraphicsNonceMetadata ?? "",
      parserMetadata: init.parserMetadata ?? "",
      reloadNavigation: init.reloadNavigation ?? false,
      historyNavigation: init.historyNavigation ?? false,
      userActivation: init.userActivation ?? false,
      taintedOrigin: init.taintedOrigin ?? false,
      redirectCount: init.redirectCount ?? 0,
      responseTainting: init.responseTainting ?? "basic",
      preventNoCacheCacheControlHeaderModification: init.preventNoCacheCacheControlHeaderModification ?? false,
      done: init.done ?? false,
      timingAllowFailed: init.timingAllowFailed ?? false,
      urlList: init.urlList,
      url: init.urlList[0],
      headersList: init.headersList ? new HeadersList(init.headersList) : new HeadersList
    };
  }
  function cloneRequest(request) {
    const newRequest = makeRequest({ ...request, body: null });
    if (request.body != null) {
      newRequest.body = cloneBody(newRequest, request.body);
    }
    return newRequest;
  }
  function fromInnerRequest(innerRequest, signal, guard) {
    const request = new Request2(kConstruct);
    request[kState] = innerRequest;
    request[kSignal] = signal;
    request[kHeaders] = new Headers2(kConstruct);
    setHeadersList(request[kHeaders], innerRequest.headersList);
    setHeadersGuard(request[kHeaders], guard);
    return request;
  }
  Object.defineProperties(Request2.prototype, {
    method: kEnumerableProperty,
    url: kEnumerableProperty,
    headers: kEnumerableProperty,
    redirect: kEnumerableProperty,
    clone: kEnumerableProperty,
    signal: kEnumerableProperty,
    duplex: kEnumerableProperty,
    destination: kEnumerableProperty,
    body: kEnumerableProperty,
    bodyUsed: kEnumerableProperty,
    isHistoryNavigation: kEnumerableProperty,
    isReloadNavigation: kEnumerableProperty,
    keepalive: kEnumerableProperty,
    integrity: kEnumerableProperty,
    cache: kEnumerableProperty,
    credentials: kEnumerableProperty,
    attribute: kEnumerableProperty,
    referrerPolicy: kEnumerableProperty,
    referrer: kEnumerableProperty,
    mode: kEnumerableProperty,
    [Symbol.toStringTag]: {
      value: "Request",
      configurable: true
    }
  });
  webidl.converters.Request = webidl.interfaceConverter(Request2);
  webidl.converters.RequestInfo = function(V, prefix, argument) {
    if (typeof V === "string") {
      return webidl.converters.USVString(V, prefix, argument);
    }
    if (V instanceof Request2) {
      return webidl.converters.Request(V, prefix, argument);
    }
    return webidl.converters.USVString(V, prefix, argument);
  };
  webidl.converters.AbortSignal = webidl.interfaceConverter(AbortSignal);
  webidl.converters.RequestInit = webidl.dictionaryConverter([
    {
      key: "method",
      converter: webidl.converters.ByteString
    },
    {
      key: "headers",
      converter: webidl.converters.HeadersInit
    },
    {
      key: "body",
      converter: webidl.nullableConverter(webidl.converters.BodyInit)
    },
    {
      key: "referrer",
      converter: webidl.converters.USVString
    },
    {
      key: "referrerPolicy",
      converter: webidl.converters.DOMString,
      allowedValues: referrerPolicy
    },
    {
      key: "mode",
      converter: webidl.converters.DOMString,
      allowedValues: requestMode
    },
    {
      key: "credentials",
      converter: webidl.converters.DOMString,
      allowedValues: requestCredentials
    },
    {
      key: "cache",
      converter: webidl.converters.DOMString,
      allowedValues: requestCache
    },
    {
      key: "redirect",
      converter: webidl.converters.DOMString,
      allowedValues: requestRedirect
    },
    {
      key: "integrity",
      converter: webidl.converters.DOMString
    },
    {
      key: "keepalive",
      converter: webidl.converters.boolean
    },
    {
      key: "signal",
      converter: webidl.nullableConverter((signal) => webidl.converters.AbortSignal(signal, "RequestInit", "signal", { strict: false }))
    },
    {
      key: "window",
      converter: webidl.converters.any
    },
    {
      key: "duplex",
      converter: webidl.converters.DOMString,
      allowedValues: requestDuplex
    },
    {
      key: "dispatcher",
      converter: webidl.converters.any
    }
  ]);
  module.exports = { Request: Request2, makeRequest, fromInnerRequest, cloneRequest };
});

// node_modules/undici/lib/web/fetch/index.js
var require_fetch = __commonJS((exports, module) => {
  var {
    makeNetworkError,
    makeAppropriateNetworkError,
    filterResponse,
    makeResponse,
    fromInnerResponse
  } = require_response();
  var { HeadersList } = require_headers();
  var { Request: Request2, cloneRequest } = require_request2();
  var zlib = __require("zlib");
  var {
    bytesMatch,
    makePolicyContainer,
    clonePolicyContainer,
    requestBadPort,
    TAOCheck,
    appendRequestOriginHeader,
    responseLocationURL,
    requestCurrentURL,
    setRequestReferrerPolicyOnRedirect,
    tryUpgradeRequestToAPotentiallyTrustworthyURL,
    createOpaqueTimingInfo,
    appendFetchMetadata,
    corsCheck,
    crossOriginResourcePolicyCheck,
    determineRequestsReferrer,
    coarsenedSharedCurrentTime,
    createDeferredPromise,
    isBlobLike,
    sameOrigin,
    isCancelled,
    isAborted,
    isErrorLike,
    fullyReadBody,
    readableStreamClose,
    isomorphicEncode,
    urlIsLocal,
    urlIsHttpHttpsScheme,
    urlHasHttpsScheme,
    clampAndCoarsenConnectionTimingInfo,
    simpleRangeHeaderValue,
    buildContentRange,
    createInflate,
    extractMimeType
  } = require_util2();
  var { kState, kDispatcher } = require_symbols2();
  var assert = __require("assert");
  var { safelyExtractBody, extractBody } = require_body();
  var {
    redirectStatusSet,
    nullBodyStatus,
    safeMethodsSet,
    requestBodyHeader,
    subresourceSet
  } = require_constants3();
  var EE = __require("events");
  var { Readable, pipeline, finished } = __require("stream");
  var { addAbortListener, isErrored, isReadable, bufferToLowerCasedHeaderName } = require_util();
  var { dataURLProcessor, serializeAMimeType, minimizeSupportedMimeType } = require_data_url();
  var { getGlobalDispatcher } = require_global2();
  var { webidl } = require_webidl();
  var { STATUS_CODES } = __require("http");
  var GET_OR_HEAD = ["GET", "HEAD"];
  var defaultUserAgent = typeof __UNDICI_IS_NODE__ !== "undefined" || typeof esbuildDetection !== "undefined" ? "node" : "undici";
  var resolveObjectURL;

  class Fetch extends EE {
    constructor(dispatcher) {
      super();
      this.dispatcher = dispatcher;
      this.connection = null;
      this.dump = false;
      this.state = "ongoing";
    }
    terminate(reason) {
      if (this.state !== "ongoing") {
        return;
      }
      this.state = "terminated";
      this.connection?.destroy(reason);
      this.emit("terminated", reason);
    }
    abort(error) {
      if (this.state !== "ongoing") {
        return;
      }
      this.state = "aborted";
      if (!error) {
        error = new DOMException("The operation was aborted.", "AbortError");
      }
      this.serializedAbortReason = error;
      this.connection?.destroy(error);
      this.emit("terminated", error);
    }
  }
  function handleFetchDone(response) {
    finalizeAndReportTiming(response, "fetch");
  }
  function fetch2(input, init = undefined) {
    webidl.argumentLengthCheck(arguments, 1, "globalThis.fetch");
    let p = createDeferredPromise();
    let requestObject;
    try {
      requestObject = new Request2(input, init);
    } catch (e) {
      p.reject(e);
      return p.promise;
    }
    const request = requestObject[kState];
    if (requestObject.signal.aborted) {
      abortFetch(p, request, null, requestObject.signal.reason);
      return p.promise;
    }
    const globalObject = request.client.globalObject;
    if (globalObject?.constructor?.name === "ServiceWorkerGlobalScope") {
      request.serviceWorkers = "none";
    }
    let responseObject = null;
    let locallyAborted = false;
    let controller = null;
    addAbortListener(requestObject.signal, () => {
      locallyAborted = true;
      assert(controller != null);
      controller.abort(requestObject.signal.reason);
      const realResponse = responseObject?.deref();
      abortFetch(p, request, realResponse, requestObject.signal.reason);
    });
    const processResponse = (response) => {
      if (locallyAborted) {
        return;
      }
      if (response.aborted) {
        abortFetch(p, request, responseObject, controller.serializedAbortReason);
        return;
      }
      if (response.type === "error") {
        p.reject(new TypeError("fetch failed", { cause: response.error }));
        return;
      }
      responseObject = new WeakRef(fromInnerResponse(response, "immutable"));
      p.resolve(responseObject.deref());
      p = null;
    };
    controller = fetching({
      request,
      processResponseEndOfBody: handleFetchDone,
      processResponse,
      dispatcher: requestObject[kDispatcher]
    });
    return p.promise;
  }
  function finalizeAndReportTiming(response, initiatorType = "other") {
    if (response.type === "error" && response.aborted) {
      return;
    }
    if (!response.urlList?.length) {
      return;
    }
    const originalURL = response.urlList[0];
    let timingInfo = response.timingInfo;
    let cacheState = response.cacheState;
    if (!urlIsHttpHttpsScheme(originalURL)) {
      return;
    }
    if (timingInfo === null) {
      return;
    }
    if (!response.timingAllowPassed) {
      timingInfo = createOpaqueTimingInfo({
        startTime: timingInfo.startTime
      });
      cacheState = "";
    }
    timingInfo.endTime = coarsenedSharedCurrentTime();
    response.timingInfo = timingInfo;
    markResourceTiming(timingInfo, originalURL.href, initiatorType, globalThis, cacheState);
  }
  var markResourceTiming = performance.markResourceTiming;
  function abortFetch(p, request, responseObject, error) {
    if (p) {
      p.reject(error);
    }
    if (request.body != null && isReadable(request.body?.stream)) {
      request.body.stream.cancel(error).catch((err) => {
        if (err.code === "ERR_INVALID_STATE") {
          return;
        }
        throw err;
      });
    }
    if (responseObject == null) {
      return;
    }
    const response = responseObject[kState];
    if (response.body != null && isReadable(response.body?.stream)) {
      response.body.stream.cancel(error).catch((err) => {
        if (err.code === "ERR_INVALID_STATE") {
          return;
        }
        throw err;
      });
    }
  }
  function fetching({
    request,
    processRequestBodyChunkLength,
    processRequestEndOfBody,
    processResponse,
    processResponseEndOfBody,
    processResponseConsumeBody,
    useParallelQueue = false,
    dispatcher = getGlobalDispatcher()
  }) {
    assert(dispatcher);
    let taskDestination = null;
    let crossOriginIsolatedCapability = false;
    if (request.client != null) {
      taskDestination = request.client.globalObject;
      crossOriginIsolatedCapability = request.client.crossOriginIsolatedCapability;
    }
    const currentTime = coarsenedSharedCurrentTime(crossOriginIsolatedCapability);
    const timingInfo = createOpaqueTimingInfo({
      startTime: currentTime
    });
    const fetchParams = {
      controller: new Fetch(dispatcher),
      request,
      timingInfo,
      processRequestBodyChunkLength,
      processRequestEndOfBody,
      processResponse,
      processResponseConsumeBody,
      processResponseEndOfBody,
      taskDestination,
      crossOriginIsolatedCapability
    };
    assert(!request.body || request.body.stream);
    if (request.window === "client") {
      request.window = request.client?.globalObject?.constructor?.name === "Window" ? request.client : "no-window";
    }
    if (request.origin === "client") {
      request.origin = request.client.origin;
    }
    if (request.policyContainer === "client") {
      if (request.client != null) {
        request.policyContainer = clonePolicyContainer(request.client.policyContainer);
      } else {
        request.policyContainer = makePolicyContainer();
      }
    }
    if (!request.headersList.contains("accept", true)) {
      const value = "*/*";
      request.headersList.append("accept", value, true);
    }
    if (!request.headersList.contains("accept-language", true)) {
      request.headersList.append("accept-language", "*", true);
    }
    if (request.priority === null) {}
    if (subresourceSet.has(request.destination)) {}
    mainFetch(fetchParams).catch((err) => {
      fetchParams.controller.terminate(err);
    });
    return fetchParams.controller;
  }
  async function mainFetch(fetchParams, recursive = false) {
    const request = fetchParams.request;
    let response = null;
    if (request.localURLsOnly && !urlIsLocal(requestCurrentURL(request))) {
      response = makeNetworkError("local URLs only");
    }
    tryUpgradeRequestToAPotentiallyTrustworthyURL(request);
    if (requestBadPort(request) === "blocked") {
      response = makeNetworkError("bad port");
    }
    if (request.referrerPolicy === "") {
      request.referrerPolicy = request.policyContainer.referrerPolicy;
    }
    if (request.referrer !== "no-referrer") {
      request.referrer = determineRequestsReferrer(request);
    }
    if (response === null) {
      response = await (async () => {
        const currentURL = requestCurrentURL(request);
        if (sameOrigin(currentURL, request.url) && request.responseTainting === "basic" || currentURL.protocol === "data:" || (request.mode === "navigate" || request.mode === "websocket")) {
          request.responseTainting = "basic";
          return await schemeFetch(fetchParams);
        }
        if (request.mode === "same-origin") {
          return makeNetworkError('request mode cannot be "same-origin"');
        }
        if (request.mode === "no-cors") {
          if (request.redirect !== "follow") {
            return makeNetworkError('redirect mode cannot be "follow" for "no-cors" request');
          }
          request.responseTainting = "opaque";
          return await schemeFetch(fetchParams);
        }
        if (!urlIsHttpHttpsScheme(requestCurrentURL(request))) {
          return makeNetworkError("URL scheme must be a HTTP(S) scheme");
        }
        request.responseTainting = "cors";
        return await httpFetch(fetchParams);
      })();
    }
    if (recursive) {
      return response;
    }
    if (response.status !== 0 && !response.internalResponse) {
      if (request.responseTainting === "cors") {}
      if (request.responseTainting === "basic") {
        response = filterResponse(response, "basic");
      } else if (request.responseTainting === "cors") {
        response = filterResponse(response, "cors");
      } else if (request.responseTainting === "opaque") {
        response = filterResponse(response, "opaque");
      } else {
        assert(false);
      }
    }
    let internalResponse = response.status === 0 ? response : response.internalResponse;
    if (internalResponse.urlList.length === 0) {
      internalResponse.urlList.push(...request.urlList);
    }
    if (!request.timingAllowFailed) {
      response.timingAllowPassed = true;
    }
    if (response.type === "opaque" && internalResponse.status === 206 && internalResponse.rangeRequested && !request.headers.contains("range", true)) {
      response = internalResponse = makeNetworkError();
    }
    if (response.status !== 0 && (request.method === "HEAD" || request.method === "CONNECT" || nullBodyStatus.includes(internalResponse.status))) {
      internalResponse.body = null;
      fetchParams.controller.dump = true;
    }
    if (request.integrity) {
      const processBodyError = (reason) => fetchFinale(fetchParams, makeNetworkError(reason));
      if (request.responseTainting === "opaque" || response.body == null) {
        processBodyError(response.error);
        return;
      }
      const processBody = (bytes) => {
        if (!bytesMatch(bytes, request.integrity)) {
          processBodyError("integrity mismatch");
          return;
        }
        response.body = safelyExtractBody(bytes)[0];
        fetchFinale(fetchParams, response);
      };
      await fullyReadBody(response.body, processBody, processBodyError);
    } else {
      fetchFinale(fetchParams, response);
    }
  }
  function schemeFetch(fetchParams) {
    if (isCancelled(fetchParams) && fetchParams.request.redirectCount === 0) {
      return Promise.resolve(makeAppropriateNetworkError(fetchParams));
    }
    const { request } = fetchParams;
    const { protocol: scheme } = requestCurrentURL(request);
    switch (scheme) {
      case "about:": {
        return Promise.resolve(makeNetworkError("about scheme is not supported"));
      }
      case "blob:": {
        if (!resolveObjectURL) {
          resolveObjectURL = __require("buffer").resolveObjectURL;
        }
        const blobURLEntry = requestCurrentURL(request);
        if (blobURLEntry.search.length !== 0) {
          return Promise.resolve(makeNetworkError("NetworkError when attempting to fetch resource."));
        }
        const blob = resolveObjectURL(blobURLEntry.toString());
        if (request.method !== "GET" || !isBlobLike(blob)) {
          return Promise.resolve(makeNetworkError("invalid method"));
        }
        const response = makeResponse();
        const fullLength = blob.size;
        const serializedFullLength = isomorphicEncode(`${fullLength}`);
        const type = blob.type;
        if (!request.headersList.contains("range", true)) {
          const bodyWithType = extractBody(blob);
          response.statusText = "OK";
          response.body = bodyWithType[0];
          response.headersList.set("content-length", serializedFullLength, true);
          response.headersList.set("content-type", type, true);
        } else {
          response.rangeRequested = true;
          const rangeHeader = request.headersList.get("range", true);
          const rangeValue = simpleRangeHeaderValue(rangeHeader, true);
          if (rangeValue === "failure") {
            return Promise.resolve(makeNetworkError("failed to fetch the data URL"));
          }
          let { rangeStartValue: rangeStart, rangeEndValue: rangeEnd } = rangeValue;
          if (rangeStart === null) {
            rangeStart = fullLength - rangeEnd;
            rangeEnd = rangeStart + rangeEnd - 1;
          } else {
            if (rangeStart >= fullLength) {
              return Promise.resolve(makeNetworkError("Range start is greater than the blob's size."));
            }
            if (rangeEnd === null || rangeEnd >= fullLength) {
              rangeEnd = fullLength - 1;
            }
          }
          const slicedBlob = blob.slice(rangeStart, rangeEnd, type);
          const slicedBodyWithType = extractBody(slicedBlob);
          response.body = slicedBodyWithType[0];
          const serializedSlicedLength = isomorphicEncode(`${slicedBlob.size}`);
          const contentRange = buildContentRange(rangeStart, rangeEnd, fullLength);
          response.status = 206;
          response.statusText = "Partial Content";
          response.headersList.set("content-length", serializedSlicedLength, true);
          response.headersList.set("content-type", type, true);
          response.headersList.set("content-range", contentRange, true);
        }
        return Promise.resolve(response);
      }
      case "data:": {
        const currentURL = requestCurrentURL(request);
        const dataURLStruct = dataURLProcessor(currentURL);
        if (dataURLStruct === "failure") {
          return Promise.resolve(makeNetworkError("failed to fetch the data URL"));
        }
        const mimeType = serializeAMimeType(dataURLStruct.mimeType);
        return Promise.resolve(makeResponse({
          statusText: "OK",
          headersList: [
            ["content-type", { name: "Content-Type", value: mimeType }]
          ],
          body: safelyExtractBody(dataURLStruct.body)[0]
        }));
      }
      case "file:": {
        return Promise.resolve(makeNetworkError("not implemented... yet..."));
      }
      case "http:":
      case "https:": {
        return httpFetch(fetchParams).catch((err) => makeNetworkError(err));
      }
      default: {
        return Promise.resolve(makeNetworkError("unknown scheme"));
      }
    }
  }
  function finalizeResponse(fetchParams, response) {
    fetchParams.request.done = true;
    if (fetchParams.processResponseDone != null) {
      queueMicrotask(() => fetchParams.processResponseDone(response));
    }
  }
  function fetchFinale(fetchParams, response) {
    let timingInfo = fetchParams.timingInfo;
    const processResponseEndOfBody = () => {
      const unsafeEndTime = Date.now();
      if (fetchParams.request.destination === "document") {
        fetchParams.controller.fullTimingInfo = timingInfo;
      }
      fetchParams.controller.reportTimingSteps = () => {
        if (fetchParams.request.url.protocol !== "https:") {
          return;
        }
        timingInfo.endTime = unsafeEndTime;
        let cacheState = response.cacheState;
        const bodyInfo = response.bodyInfo;
        if (!response.timingAllowPassed) {
          timingInfo = createOpaqueTimingInfo(timingInfo);
          cacheState = "";
        }
        let responseStatus = 0;
        if (fetchParams.request.mode !== "navigator" || !response.hasCrossOriginRedirects) {
          responseStatus = response.status;
          const mimeType = extractMimeType(response.headersList);
          if (mimeType !== "failure") {
            bodyInfo.contentType = minimizeSupportedMimeType(mimeType);
          }
        }
        if (fetchParams.request.initiatorType != null) {
          markResourceTiming(timingInfo, fetchParams.request.url.href, fetchParams.request.initiatorType, globalThis, cacheState, bodyInfo, responseStatus);
        }
      };
      const processResponseEndOfBodyTask = () => {
        fetchParams.request.done = true;
        if (fetchParams.processResponseEndOfBody != null) {
          queueMicrotask(() => fetchParams.processResponseEndOfBody(response));
        }
        if (fetchParams.request.initiatorType != null) {
          fetchParams.controller.reportTimingSteps();
        }
      };
      queueMicrotask(() => processResponseEndOfBodyTask());
    };
    if (fetchParams.processResponse != null) {
      queueMicrotask(() => {
        fetchParams.processResponse(response);
        fetchParams.processResponse = null;
      });
    }
    const internalResponse = response.type === "error" ? response : response.internalResponse ?? response;
    if (internalResponse.body == null) {
      processResponseEndOfBody();
    } else {
      finished(internalResponse.body.stream, () => {
        processResponseEndOfBody();
      });
    }
  }
  async function httpFetch(fetchParams) {
    const request = fetchParams.request;
    let response = null;
    let actualResponse = null;
    const timingInfo = fetchParams.timingInfo;
    if (request.serviceWorkers === "all") {}
    if (response === null) {
      if (request.redirect === "follow") {
        request.serviceWorkers = "none";
      }
      actualResponse = response = await httpNetworkOrCacheFetch(fetchParams);
      if (request.responseTainting === "cors" && corsCheck(request, response) === "failure") {
        return makeNetworkError("cors failure");
      }
      if (TAOCheck(request, response) === "failure") {
        request.timingAllowFailed = true;
      }
    }
    if ((request.responseTainting === "opaque" || response.type === "opaque") && crossOriginResourcePolicyCheck(request.origin, request.client, request.destination, actualResponse) === "blocked") {
      return makeNetworkError("blocked");
    }
    if (redirectStatusSet.has(actualResponse.status)) {
      if (request.redirect !== "manual") {
        fetchParams.controller.connection.destroy(undefined, false);
      }
      if (request.redirect === "error") {
        response = makeNetworkError("unexpected redirect");
      } else if (request.redirect === "manual") {
        response = actualResponse;
      } else if (request.redirect === "follow") {
        response = await httpRedirectFetch(fetchParams, response);
      } else {
        assert(false);
      }
    }
    response.timingInfo = timingInfo;
    return response;
  }
  function httpRedirectFetch(fetchParams, response) {
    const request = fetchParams.request;
    const actualResponse = response.internalResponse ? response.internalResponse : response;
    let locationURL;
    try {
      locationURL = responseLocationURL(actualResponse, requestCurrentURL(request).hash);
      if (locationURL == null) {
        return response;
      }
    } catch (err) {
      return Promise.resolve(makeNetworkError(err));
    }
    if (!urlIsHttpHttpsScheme(locationURL)) {
      return Promise.resolve(makeNetworkError("URL scheme must be a HTTP(S) scheme"));
    }
    if (request.redirectCount === 20) {
      return Promise.resolve(makeNetworkError("redirect count exceeded"));
    }
    request.redirectCount += 1;
    if (request.mode === "cors" && (locationURL.username || locationURL.password) && !sameOrigin(request, locationURL)) {
      return Promise.resolve(makeNetworkError('cross origin not allowed for request mode "cors"'));
    }
    if (request.responseTainting === "cors" && (locationURL.username || locationURL.password)) {
      return Promise.resolve(makeNetworkError('URL cannot contain credentials for request mode "cors"'));
    }
    if (actualResponse.status !== 303 && request.body != null && request.body.source == null) {
      return Promise.resolve(makeNetworkError());
    }
    if ([301, 302].includes(actualResponse.status) && request.method === "POST" || actualResponse.status === 303 && !GET_OR_HEAD.includes(request.method)) {
      request.method = "GET";
      request.body = null;
      for (const headerName of requestBodyHeader) {
        request.headersList.delete(headerName);
      }
    }
    if (!sameOrigin(requestCurrentURL(request), locationURL)) {
      request.headersList.delete("authorization", true);
      request.headersList.delete("proxy-authorization", true);
      request.headersList.delete("cookie", true);
      request.headersList.delete("host", true);
    }
    if (request.body != null) {
      assert(request.body.source != null);
      request.body = safelyExtractBody(request.body.source)[0];
    }
    const timingInfo = fetchParams.timingInfo;
    timingInfo.redirectEndTime = timingInfo.postRedirectStartTime = coarsenedSharedCurrentTime(fetchParams.crossOriginIsolatedCapability);
    if (timingInfo.redirectStartTime === 0) {
      timingInfo.redirectStartTime = timingInfo.startTime;
    }
    request.urlList.push(locationURL);
    setRequestReferrerPolicyOnRedirect(request, actualResponse);
    return mainFetch(fetchParams, true);
  }
  async function httpNetworkOrCacheFetch(fetchParams, isAuthenticationFetch = false, isNewConnectionFetch = false) {
    const request = fetchParams.request;
    let httpFetchParams = null;
    let httpRequest = null;
    let response = null;
    const httpCache = null;
    const revalidatingFlag = false;
    if (request.window === "no-window" && request.redirect === "error") {
      httpFetchParams = fetchParams;
      httpRequest = request;
    } else {
      httpRequest = cloneRequest(request);
      httpFetchParams = { ...fetchParams };
      httpFetchParams.request = httpRequest;
    }
    const includeCredentials = request.credentials === "include" || request.credentials === "same-origin" && request.responseTainting === "basic";
    const contentLength = httpRequest.body ? httpRequest.body.length : null;
    let contentLengthHeaderValue = null;
    if (httpRequest.body == null && ["POST", "PUT"].includes(httpRequest.method)) {
      contentLengthHeaderValue = "0";
    }
    if (contentLength != null) {
      contentLengthHeaderValue = isomorphicEncode(`${contentLength}`);
    }
    if (contentLengthHeaderValue != null) {
      httpRequest.headersList.append("content-length", contentLengthHeaderValue, true);
    }
    if (contentLength != null && httpRequest.keepalive) {}
    if (httpRequest.referrer instanceof URL) {
      httpRequest.headersList.append("referer", isomorphicEncode(httpRequest.referrer.href), true);
    }
    appendRequestOriginHeader(httpRequest);
    appendFetchMetadata(httpRequest);
    if (!httpRequest.headersList.contains("user-agent", true)) {
      httpRequest.headersList.append("user-agent", defaultUserAgent);
    }
    if (httpRequest.cache === "default" && (httpRequest.headersList.contains("if-modified-since", true) || httpRequest.headersList.contains("if-none-match", true) || httpRequest.headersList.contains("if-unmodified-since", true) || httpRequest.headersList.contains("if-match", true) || httpRequest.headersList.contains("if-range", true))) {
      httpRequest.cache = "no-store";
    }
    if (httpRequest.cache === "no-cache" && !httpRequest.preventNoCacheCacheControlHeaderModification && !httpRequest.headersList.contains("cache-control", true)) {
      httpRequest.headersList.append("cache-control", "max-age=0", true);
    }
    if (httpRequest.cache === "no-store" || httpRequest.cache === "reload") {
      if (!httpRequest.headersList.contains("pragma", true)) {
        httpRequest.headersList.append("pragma", "no-cache", true);
      }
      if (!httpRequest.headersList.contains("cache-control", true)) {
        httpRequest.headersList.append("cache-control", "no-cache", true);
      }
    }
    if (httpRequest.headersList.contains("range", true)) {
      httpRequest.headersList.append("accept-encoding", "identity", true);
    }
    if (!httpRequest.headersList.contains("accept-encoding", true)) {
      if (urlHasHttpsScheme(requestCurrentURL(httpRequest))) {
        httpRequest.headersList.append("accept-encoding", "br, gzip, deflate", true);
      } else {
        httpRequest.headersList.append("accept-encoding", "gzip, deflate", true);
      }
    }
    httpRequest.headersList.delete("host", true);
    if (includeCredentials) {}
    if (httpCache == null) {
      httpRequest.cache = "no-store";
    }
    if (httpRequest.cache !== "no-store" && httpRequest.cache !== "reload") {}
    if (response == null) {
      if (httpRequest.cache === "only-if-cached") {
        return makeNetworkError("only if cached");
      }
      const forwardResponse = await httpNetworkFetch(httpFetchParams, includeCredentials, isNewConnectionFetch);
      if (!safeMethodsSet.has(httpRequest.method) && forwardResponse.status >= 200 && forwardResponse.status <= 399) {}
      if (revalidatingFlag && forwardResponse.status === 304) {}
      if (response == null) {
        response = forwardResponse;
      }
    }
    response.urlList = [...httpRequest.urlList];
    if (httpRequest.headersList.contains("range", true)) {
      response.rangeRequested = true;
    }
    response.requestIncludesCredentials = includeCredentials;
    if (response.status === 407) {
      if (request.window === "no-window") {
        return makeNetworkError();
      }
      if (isCancelled(fetchParams)) {
        return makeAppropriateNetworkError(fetchParams);
      }
      return makeNetworkError("proxy authentication required");
    }
    if (response.status === 421 && !isNewConnectionFetch && (request.body == null || request.body.source != null)) {
      if (isCancelled(fetchParams)) {
        return makeAppropriateNetworkError(fetchParams);
      }
      fetchParams.controller.connection.destroy();
      response = await httpNetworkOrCacheFetch(fetchParams, isAuthenticationFetch, true);
    }
    if (isAuthenticationFetch) {}
    return response;
  }
  async function httpNetworkFetch(fetchParams, includeCredentials = false, forceNewConnection = false) {
    assert(!fetchParams.controller.connection || fetchParams.controller.connection.destroyed);
    fetchParams.controller.connection = {
      abort: null,
      destroyed: false,
      destroy(err, abort = true) {
        if (!this.destroyed) {
          this.destroyed = true;
          if (abort) {
            this.abort?.(err ?? new DOMException("The operation was aborted.", "AbortError"));
          }
        }
      }
    };
    const request = fetchParams.request;
    let response = null;
    const timingInfo = fetchParams.timingInfo;
    const httpCache = null;
    if (httpCache == null) {
      request.cache = "no-store";
    }
    const newConnection = forceNewConnection ? "yes" : "no";
    if (request.mode === "websocket") {}
    let requestBody = null;
    if (request.body == null && fetchParams.processRequestEndOfBody) {
      queueMicrotask(() => fetchParams.processRequestEndOfBody());
    } else if (request.body != null) {
      const processBodyChunk = async function* (bytes) {
        if (isCancelled(fetchParams)) {
          return;
        }
        yield bytes;
        fetchParams.processRequestBodyChunkLength?.(bytes.byteLength);
      };
      const processEndOfBody = () => {
        if (isCancelled(fetchParams)) {
          return;
        }
        if (fetchParams.processRequestEndOfBody) {
          fetchParams.processRequestEndOfBody();
        }
      };
      const processBodyError = (e) => {
        if (isCancelled(fetchParams)) {
          return;
        }
        if (e.name === "AbortError") {
          fetchParams.controller.abort();
        } else {
          fetchParams.controller.terminate(e);
        }
      };
      requestBody = async function* () {
        try {
          for await (const bytes of request.body.stream) {
            yield* processBodyChunk(bytes);
          }
          processEndOfBody();
        } catch (err) {
          processBodyError(err);
        }
      }();
    }
    try {
      const { body, status, statusText, headersList, socket } = await dispatch({ body: requestBody });
      if (socket) {
        response = makeResponse({ status, statusText, headersList, socket });
      } else {
        const iterator = body[Symbol.asyncIterator]();
        fetchParams.controller.next = () => iterator.next();
        response = makeResponse({ status, statusText, headersList });
      }
    } catch (err) {
      if (err.name === "AbortError") {
        fetchParams.controller.connection.destroy();
        return makeAppropriateNetworkError(fetchParams, err);
      }
      return makeNetworkError(err);
    }
    const pullAlgorithm = async () => {
      await fetchParams.controller.resume();
    };
    const cancelAlgorithm = (reason) => {
      if (!isCancelled(fetchParams)) {
        fetchParams.controller.abort(reason);
      }
    };
    const stream = new ReadableStream({
      async start(controller) {
        fetchParams.controller.controller = controller;
      },
      async pull(controller) {
        await pullAlgorithm(controller);
      },
      async cancel(reason) {
        await cancelAlgorithm(reason);
      },
      type: "bytes"
    });
    response.body = { stream, source: null, length: null };
    fetchParams.controller.onAborted = onAborted;
    fetchParams.controller.on("terminated", onAborted);
    fetchParams.controller.resume = async () => {
      while (true) {
        let bytes;
        let isFailure;
        try {
          const { done, value } = await fetchParams.controller.next();
          if (isAborted(fetchParams)) {
            break;
          }
          bytes = done ? undefined : value;
        } catch (err) {
          if (fetchParams.controller.ended && !timingInfo.encodedBodySize) {
            bytes = undefined;
          } else {
            bytes = err;
            isFailure = true;
          }
        }
        if (bytes === undefined) {
          readableStreamClose(fetchParams.controller.controller);
          finalizeResponse(fetchParams, response);
          return;
        }
        timingInfo.decodedBodySize += bytes?.byteLength ?? 0;
        if (isFailure) {
          fetchParams.controller.terminate(bytes);
          return;
        }
        const buffer = new Uint8Array(bytes);
        if (buffer.byteLength) {
          fetchParams.controller.controller.enqueue(buffer);
        }
        if (isErrored(stream)) {
          fetchParams.controller.terminate();
          return;
        }
        if (fetchParams.controller.controller.desiredSize <= 0) {
          return;
        }
      }
    };
    function onAborted(reason) {
      if (isAborted(fetchParams)) {
        response.aborted = true;
        if (isReadable(stream)) {
          fetchParams.controller.controller.error(fetchParams.controller.serializedAbortReason);
        }
      } else {
        if (isReadable(stream)) {
          fetchParams.controller.controller.error(new TypeError("terminated", {
            cause: isErrorLike(reason) ? reason : undefined
          }));
        }
      }
      fetchParams.controller.connection.destroy();
    }
    return response;
    function dispatch({ body }) {
      const url = requestCurrentURL(request);
      const agent = fetchParams.controller.dispatcher;
      return new Promise((resolve3, reject) => agent.dispatch({
        path: url.pathname + url.search,
        origin: url.origin,
        method: request.method,
        body: agent.isMockActive ? request.body && (request.body.source || request.body.stream) : body,
        headers: request.headersList.entries,
        maxRedirections: 0,
        upgrade: request.mode === "websocket" ? "websocket" : undefined
      }, {
        body: null,
        abort: null,
        onConnect(abort) {
          const { connection } = fetchParams.controller;
          timingInfo.finalConnectionTimingInfo = clampAndCoarsenConnectionTimingInfo(undefined, timingInfo.postRedirectStartTime, fetchParams.crossOriginIsolatedCapability);
          if (connection.destroyed) {
            abort(new DOMException("The operation was aborted.", "AbortError"));
          } else {
            fetchParams.controller.on("terminated", abort);
            this.abort = connection.abort = abort;
          }
          timingInfo.finalNetworkRequestStartTime = coarsenedSharedCurrentTime(fetchParams.crossOriginIsolatedCapability);
        },
        onResponseStarted() {
          timingInfo.finalNetworkResponseStartTime = coarsenedSharedCurrentTime(fetchParams.crossOriginIsolatedCapability);
        },
        onHeaders(status, rawHeaders, resume, statusText) {
          if (status < 200) {
            return;
          }
          let location = "";
          const headersList = new HeadersList;
          for (let i = 0;i < rawHeaders.length; i += 2) {
            headersList.append(bufferToLowerCasedHeaderName(rawHeaders[i]), rawHeaders[i + 1].toString("latin1"), true);
          }
          location = headersList.get("location", true);
          this.body = new Readable({ read: resume });
          const decoders = [];
          const willFollow = location && request.redirect === "follow" && redirectStatusSet.has(status);
          if (request.method !== "HEAD" && request.method !== "CONNECT" && !nullBodyStatus.includes(status) && !willFollow) {
            const contentEncoding = headersList.get("content-encoding", true);
            const codings = contentEncoding ? contentEncoding.toLowerCase().split(",") : [];
            const maxContentEncodings = 5;
            if (codings.length > maxContentEncodings) {
              reject(new Error(`too many content-encodings in response: ${codings.length}, maximum allowed is ${maxContentEncodings}`));
              return true;
            }
            for (let i = codings.length - 1;i >= 0; --i) {
              const coding = codings[i].trim();
              if (coding === "x-gzip" || coding === "gzip") {
                decoders.push(zlib.createGunzip({
                  flush: zlib.constants.Z_SYNC_FLUSH,
                  finishFlush: zlib.constants.Z_SYNC_FLUSH
                }));
              } else if (coding === "deflate") {
                decoders.push(createInflate({
                  flush: zlib.constants.Z_SYNC_FLUSH,
                  finishFlush: zlib.constants.Z_SYNC_FLUSH
                }));
              } else if (coding === "br") {
                decoders.push(zlib.createBrotliDecompress({
                  flush: zlib.constants.BROTLI_OPERATION_FLUSH,
                  finishFlush: zlib.constants.BROTLI_OPERATION_FLUSH
                }));
              } else {
                decoders.length = 0;
                break;
              }
            }
          }
          const onError = this.onError.bind(this);
          resolve3({
            status,
            statusText,
            headersList,
            body: decoders.length ? pipeline(this.body, ...decoders, (err) => {
              if (err) {
                this.onError(err);
              }
            }).on("error", onError) : this.body.on("error", onError)
          });
          return true;
        },
        onData(chunk) {
          if (fetchParams.controller.dump) {
            return;
          }
          const bytes = chunk;
          timingInfo.encodedBodySize += bytes.byteLength;
          return this.body.push(bytes);
        },
        onComplete() {
          if (this.abort) {
            fetchParams.controller.off("terminated", this.abort);
          }
          if (fetchParams.controller.onAborted) {
            fetchParams.controller.off("terminated", fetchParams.controller.onAborted);
          }
          fetchParams.controller.ended = true;
          this.body.push(null);
        },
        onError(error) {
          if (this.abort) {
            fetchParams.controller.off("terminated", this.abort);
          }
          this.body?.destroy(error);
          fetchParams.controller.terminate(error);
          reject(error);
        },
        onUpgrade(status, rawHeaders, socket) {
          if (status !== 101) {
            return;
          }
          const headersList = new HeadersList;
          for (let i = 0;i < rawHeaders.length; i += 2) {
            headersList.append(bufferToLowerCasedHeaderName(rawHeaders[i]), rawHeaders[i + 1].toString("latin1"), true);
          }
          resolve3({
            status,
            statusText: STATUS_CODES[status],
            headersList,
            socket
          });
          return true;
        }
      }));
    }
  }
  module.exports = {
    fetch: fetch2,
    Fetch,
    fetching,
    finalizeAndReportTiming
  };
});

// node_modules/undici/lib/web/fileapi/symbols.js
var require_symbols3 = __commonJS((exports, module) => {
  module.exports = {
    kState: Symbol("FileReader state"),
    kResult: Symbol("FileReader result"),
    kError: Symbol("FileReader error"),
    kLastProgressEventFired: Symbol("FileReader last progress event fired timestamp"),
    kEvents: Symbol("FileReader events"),
    kAborted: Symbol("FileReader aborted")
  };
});

// node_modules/undici/lib/web/fileapi/progressevent.js
var require_progressevent = __commonJS((exports, module) => {
  var { webidl } = require_webidl();
  var kState = Symbol("ProgressEvent state");

  class ProgressEvent extends Event {
    constructor(type, eventInitDict = {}) {
      type = webidl.converters.DOMString(type, "ProgressEvent constructor", "type");
      eventInitDict = webidl.converters.ProgressEventInit(eventInitDict ?? {});
      super(type, eventInitDict);
      this[kState] = {
        lengthComputable: eventInitDict.lengthComputable,
        loaded: eventInitDict.loaded,
        total: eventInitDict.total
      };
    }
    get lengthComputable() {
      webidl.brandCheck(this, ProgressEvent);
      return this[kState].lengthComputable;
    }
    get loaded() {
      webidl.brandCheck(this, ProgressEvent);
      return this[kState].loaded;
    }
    get total() {
      webidl.brandCheck(this, ProgressEvent);
      return this[kState].total;
    }
  }
  webidl.converters.ProgressEventInit = webidl.dictionaryConverter([
    {
      key: "lengthComputable",
      converter: webidl.converters.boolean,
      defaultValue: () => false
    },
    {
      key: "loaded",
      converter: webidl.converters["unsigned long long"],
      defaultValue: () => 0
    },
    {
      key: "total",
      converter: webidl.converters["unsigned long long"],
      defaultValue: () => 0
    },
    {
      key: "bubbles",
      converter: webidl.converters.boolean,
      defaultValue: () => false
    },
    {
      key: "cancelable",
      converter: webidl.converters.boolean,
      defaultValue: () => false
    },
    {
      key: "composed",
      converter: webidl.converters.boolean,
      defaultValue: () => false
    }
  ]);
  module.exports = {
    ProgressEvent
  };
});

// node_modules/undici/lib/web/fileapi/encoding.js
var require_encoding = __commonJS((exports, module) => {
  function getEncoding(label) {
    if (!label) {
      return "failure";
    }
    switch (label.trim().toLowerCase()) {
      case "unicode-1-1-utf-8":
      case "unicode11utf8":
      case "unicode20utf8":
      case "utf-8":
      case "utf8":
      case "x-unicode20utf8":
        return "UTF-8";
      case "866":
      case "cp866":
      case "csibm866":
      case "ibm866":
        return "IBM866";
      case "csisolatin2":
      case "iso-8859-2":
      case "iso-ir-101":
      case "iso8859-2":
      case "iso88592":
      case "iso_8859-2":
      case "iso_8859-2:1987":
      case "l2":
      case "latin2":
        return "ISO-8859-2";
      case "csisolatin3":
      case "iso-8859-3":
      case "iso-ir-109":
      case "iso8859-3":
      case "iso88593":
      case "iso_8859-3":
      case "iso_8859-3:1988":
      case "l3":
      case "latin3":
        return "ISO-8859-3";
      case "csisolatin4":
      case "iso-8859-4":
      case "iso-ir-110":
      case "iso8859-4":
      case "iso88594":
      case "iso_8859-4":
      case "iso_8859-4:1988":
      case "l4":
      case "latin4":
        return "ISO-8859-4";
      case "csisolatincyrillic":
      case "cyrillic":
      case "iso-8859-5":
      case "iso-ir-144":
      case "iso8859-5":
      case "iso88595":
      case "iso_8859-5":
      case "iso_8859-5:1988":
        return "ISO-8859-5";
      case "arabic":
      case "asmo-708":
      case "csiso88596e":
      case "csiso88596i":
      case "csisolatinarabic":
      case "ecma-114":
      case "iso-8859-6":
      case "iso-8859-6-e":
      case "iso-8859-6-i":
      case "iso-ir-127":
      case "iso8859-6":
      case "iso88596":
      case "iso_8859-6":
      case "iso_8859-6:1987":
        return "ISO-8859-6";
      case "csisolatingreek":
      case "ecma-118":
      case "elot_928":
      case "greek":
      case "greek8":
      case "iso-8859-7":
      case "iso-ir-126":
      case "iso8859-7":
      case "iso88597":
      case "iso_8859-7":
      case "iso_8859-7:1987":
      case "sun_eu_greek":
        return "ISO-8859-7";
      case "csiso88598e":
      case "csisolatinhebrew":
      case "hebrew":
      case "iso-8859-8":
      case "iso-8859-8-e":
      case "iso-ir-138":
      case "iso8859-8":
      case "iso88598":
      case "iso_8859-8":
      case "iso_8859-8:1988":
      case "visual":
        return "ISO-8859-8";
      case "csiso88598i":
      case "iso-8859-8-i":
      case "logical":
        return "ISO-8859-8-I";
      case "csisolatin6":
      case "iso-8859-10":
      case "iso-ir-157":
      case "iso8859-10":
      case "iso885910":
      case "l6":
      case "latin6":
        return "ISO-8859-10";
      case "iso-8859-13":
      case "iso8859-13":
      case "iso885913":
        return "ISO-8859-13";
      case "iso-8859-14":
      case "iso8859-14":
      case "iso885914":
        return "ISO-8859-14";
      case "csisolatin9":
      case "iso-8859-15":
      case "iso8859-15":
      case "iso885915":
      case "iso_8859-15":
      case "l9":
        return "ISO-8859-15";
      case "iso-8859-16":
        return "ISO-8859-16";
      case "cskoi8r":
      case "koi":
      case "koi8":
      case "koi8-r":
      case "koi8_r":
        return "KOI8-R";
      case "koi8-ru":
      case "koi8-u":
        return "KOI8-U";
      case "csmacintosh":
      case "mac":
      case "macintosh":
      case "x-mac-roman":
        return "macintosh";
      case "iso-8859-11":
      case "iso8859-11":
      case "iso885911":
      case "tis-620":
      case "windows-874":
        return "windows-874";
      case "cp1250":
      case "windows-1250":
      case "x-cp1250":
        return "windows-1250";
      case "cp1251":
      case "windows-1251":
      case "x-cp1251":
        return "windows-1251";
      case "ansi_x3.4-1968":
      case "ascii":
      case "cp1252":
      case "cp819":
      case "csisolatin1":
      case "ibm819":
      case "iso-8859-1":
      case "iso-ir-100":
      case "iso8859-1":
      case "iso88591":
      case "iso_8859-1":
      case "iso_8859-1:1987":
      case "l1":
      case "latin1":
      case "us-ascii":
      case "windows-1252":
      case "x-cp1252":
        return "windows-1252";
      case "cp1253":
      case "windows-1253":
      case "x-cp1253":
        return "windows-1253";
      case "cp1254":
      case "csisolatin5":
      case "iso-8859-9":
      case "iso-ir-148":
      case "iso8859-9":
      case "iso88599":
      case "iso_8859-9":
      case "iso_8859-9:1989":
      case "l5":
      case "latin5":
      case "windows-1254":
      case "x-cp1254":
        return "windows-1254";
      case "cp1255":
      case "windows-1255":
      case "x-cp1255":
        return "windows-1255";
      case "cp1256":
      case "windows-1256":
      case "x-cp1256":
        return "windows-1256";
      case "cp1257":
      case "windows-1257":
      case "x-cp1257":
        return "windows-1257";
      case "cp1258":
      case "windows-1258":
      case "x-cp1258":
        return "windows-1258";
      case "x-mac-cyrillic":
      case "x-mac-ukrainian":
        return "x-mac-cyrillic";
      case "chinese":
      case "csgb2312":
      case "csiso58gb231280":
      case "gb2312":
      case "gb_2312":
      case "gb_2312-80":
      case "gbk":
      case "iso-ir-58":
      case "x-gbk":
        return "GBK";
      case "gb18030":
        return "gb18030";
      case "big5":
      case "big5-hkscs":
      case "cn-big5":
      case "csbig5":
      case "x-x-big5":
        return "Big5";
      case "cseucpkdfmtjapanese":
      case "euc-jp":
      case "x-euc-jp":
        return "EUC-JP";
      case "csiso2022jp":
      case "iso-2022-jp":
        return "ISO-2022-JP";
      case "csshiftjis":
      case "ms932":
      case "ms_kanji":
      case "shift-jis":
      case "shift_jis":
      case "sjis":
      case "windows-31j":
      case "x-sjis":
        return "Shift_JIS";
      case "cseuckr":
      case "csksc56011987":
      case "euc-kr":
      case "iso-ir-149":
      case "korean":
      case "ks_c_5601-1987":
      case "ks_c_5601-1989":
      case "ksc5601":
      case "ksc_5601":
      case "windows-949":
        return "EUC-KR";
      case "csiso2022kr":
      case "hz-gb-2312":
      case "iso-2022-cn":
      case "iso-2022-cn-ext":
      case "iso-2022-kr":
      case "replacement":
        return "replacement";
      case "unicodefffe":
      case "utf-16be":
        return "UTF-16BE";
      case "csunicode":
      case "iso-10646-ucs-2":
      case "ucs-2":
      case "unicode":
      case "unicodefeff":
      case "utf-16":
      case "utf-16le":
        return "UTF-16LE";
      case "x-user-defined":
        return "x-user-defined";
      default:
        return "failure";
    }
  }
  module.exports = {
    getEncoding
  };
});

// node_modules/undici/lib/web/fileapi/util.js
var require_util4 = __commonJS((exports, module) => {
  var {
    kState,
    kError,
    kResult,
    kAborted,
    kLastProgressEventFired
  } = require_symbols3();
  var { ProgressEvent } = require_progressevent();
  var { getEncoding } = require_encoding();
  var { serializeAMimeType, parseMIMEType } = require_data_url();
  var { types } = __require("util");
  var { StringDecoder } = __require("string_decoder");
  var { btoa } = __require("buffer");
  var staticPropertyDescriptors = {
    enumerable: true,
    writable: false,
    configurable: false
  };
  function readOperation(fr, blob, type, encodingName) {
    if (fr[kState] === "loading") {
      throw new DOMException("Invalid state", "InvalidStateError");
    }
    fr[kState] = "loading";
    fr[kResult] = null;
    fr[kError] = null;
    const stream = blob.stream();
    const reader = stream.getReader();
    const bytes = [];
    let chunkPromise = reader.read();
    let isFirstChunk = true;
    (async () => {
      while (!fr[kAborted]) {
        try {
          const { done, value } = await chunkPromise;
          if (isFirstChunk && !fr[kAborted]) {
            queueMicrotask(() => {
              fireAProgressEvent("loadstart", fr);
            });
          }
          isFirstChunk = false;
          if (!done && types.isUint8Array(value)) {
            bytes.push(value);
            if ((fr[kLastProgressEventFired] === undefined || Date.now() - fr[kLastProgressEventFired] >= 50) && !fr[kAborted]) {
              fr[kLastProgressEventFired] = Date.now();
              queueMicrotask(() => {
                fireAProgressEvent("progress", fr);
              });
            }
            chunkPromise = reader.read();
          } else if (done) {
            queueMicrotask(() => {
              fr[kState] = "done";
              try {
                const result = packageData(bytes, type, blob.type, encodingName);
                if (fr[kAborted]) {
                  return;
                }
                fr[kResult] = result;
                fireAProgressEvent("load", fr);
              } catch (error) {
                fr[kError] = error;
                fireAProgressEvent("error", fr);
              }
              if (fr[kState] !== "loading") {
                fireAProgressEvent("loadend", fr);
              }
            });
            break;
          }
        } catch (error) {
          if (fr[kAborted]) {
            return;
          }
          queueMicrotask(() => {
            fr[kState] = "done";
            fr[kError] = error;
            fireAProgressEvent("error", fr);
            if (fr[kState] !== "loading") {
              fireAProgressEvent("loadend", fr);
            }
          });
          break;
        }
      }
    })();
  }
  function fireAProgressEvent(e, reader) {
    const event = new ProgressEvent(e, {
      bubbles: false,
      cancelable: false
    });
    reader.dispatchEvent(event);
  }
  function packageData(bytes, type, mimeType, encodingName) {
    switch (type) {
      case "DataURL": {
        let dataURL = "data:";
        const parsed = parseMIMEType(mimeType || "application/octet-stream");
        if (parsed !== "failure") {
          dataURL += serializeAMimeType(parsed);
        }
        dataURL += ";base64,";
        const decoder = new StringDecoder("latin1");
        for (const chunk of bytes) {
          dataURL += btoa(decoder.write(chunk));
        }
        dataURL += btoa(decoder.end());
        return dataURL;
      }
      case "Text": {
        let encoding = "failure";
        if (encodingName) {
          encoding = getEncoding(encodingName);
        }
        if (encoding === "failure" && mimeType) {
          const type2 = parseMIMEType(mimeType);
          if (type2 !== "failure") {
            encoding = getEncoding(type2.parameters.get("charset"));
          }
        }
        if (encoding === "failure") {
          encoding = "UTF-8";
        }
        return decode(bytes, encoding);
      }
      case "ArrayBuffer": {
        const sequence = combineByteSequences(bytes);
        return sequence.buffer;
      }
      case "BinaryString": {
        let binaryString = "";
        const decoder = new StringDecoder("latin1");
        for (const chunk of bytes) {
          binaryString += decoder.write(chunk);
        }
        binaryString += decoder.end();
        return binaryString;
      }
    }
  }
  function decode(ioQueue, encoding) {
    const bytes = combineByteSequences(ioQueue);
    const BOMEncoding = BOMSniffing(bytes);
    let slice = 0;
    if (BOMEncoding !== null) {
      encoding = BOMEncoding;
      slice = BOMEncoding === "UTF-8" ? 3 : 2;
    }
    const sliced = bytes.slice(slice);
    return new TextDecoder(encoding).decode(sliced);
  }
  function BOMSniffing(ioQueue) {
    const [a, b, c] = ioQueue;
    if (a === 239 && b === 187 && c === 191) {
      return "UTF-8";
    } else if (a === 254 && b === 255) {
      return "UTF-16BE";
    } else if (a === 255 && b === 254) {
      return "UTF-16LE";
    }
    return null;
  }
  function combineByteSequences(sequences) {
    const size = sequences.reduce((a, b) => {
      return a + b.byteLength;
    }, 0);
    let offset = 0;
    return sequences.reduce((a, b) => {
      a.set(b, offset);
      offset += b.byteLength;
      return a;
    }, new Uint8Array(size));
  }
  module.exports = {
    staticPropertyDescriptors,
    readOperation,
    fireAProgressEvent
  };
});

// node_modules/undici/lib/web/fileapi/filereader.js
var require_filereader = __commonJS((exports, module) => {
  var {
    staticPropertyDescriptors,
    readOperation,
    fireAProgressEvent
  } = require_util4();
  var {
    kState,
    kError,
    kResult,
    kEvents,
    kAborted
  } = require_symbols3();
  var { webidl } = require_webidl();
  var { kEnumerableProperty } = require_util();

  class FileReader extends EventTarget {
    constructor() {
      super();
      this[kState] = "empty";
      this[kResult] = null;
      this[kError] = null;
      this[kEvents] = {
        loadend: null,
        error: null,
        abort: null,
        load: null,
        progress: null,
        loadstart: null
      };
    }
    readAsArrayBuffer(blob) {
      webidl.brandCheck(this, FileReader);
      webidl.argumentLengthCheck(arguments, 1, "FileReader.readAsArrayBuffer");
      blob = webidl.converters.Blob(blob, { strict: false });
      readOperation(this, blob, "ArrayBuffer");
    }
    readAsBinaryString(blob) {
      webidl.brandCheck(this, FileReader);
      webidl.argumentLengthCheck(arguments, 1, "FileReader.readAsBinaryString");
      blob = webidl.converters.Blob(blob, { strict: false });
      readOperation(this, blob, "BinaryString");
    }
    readAsText(blob, encoding = undefined) {
      webidl.brandCheck(this, FileReader);
      webidl.argumentLengthCheck(arguments, 1, "FileReader.readAsText");
      blob = webidl.converters.Blob(blob, { strict: false });
      if (encoding !== undefined) {
        encoding = webidl.converters.DOMString(encoding, "FileReader.readAsText", "encoding");
      }
      readOperation(this, blob, "Text", encoding);
    }
    readAsDataURL(blob) {
      webidl.brandCheck(this, FileReader);
      webidl.argumentLengthCheck(arguments, 1, "FileReader.readAsDataURL");
      blob = webidl.converters.Blob(blob, { strict: false });
      readOperation(this, blob, "DataURL");
    }
    abort() {
      if (this[kState] === "empty" || this[kState] === "done") {
        this[kResult] = null;
        return;
      }
      if (this[kState] === "loading") {
        this[kState] = "done";
        this[kResult] = null;
      }
      this[kAborted] = true;
      fireAProgressEvent("abort", this);
      if (this[kState] !== "loading") {
        fireAProgressEvent("loadend", this);
      }
    }
    get readyState() {
      webidl.brandCheck(this, FileReader);
      switch (this[kState]) {
        case "empty":
          return this.EMPTY;
        case "loading":
          return this.LOADING;
        case "done":
          return this.DONE;
      }
    }
    get result() {
      webidl.brandCheck(this, FileReader);
      return this[kResult];
    }
    get error() {
      webidl.brandCheck(this, FileReader);
      return this[kError];
    }
    get onloadend() {
      webidl.brandCheck(this, FileReader);
      return this[kEvents].loadend;
    }
    set onloadend(fn) {
      webidl.brandCheck(this, FileReader);
      if (this[kEvents].loadend) {
        this.removeEventListener("loadend", this[kEvents].loadend);
      }
      if (typeof fn === "function") {
        this[kEvents].loadend = fn;
        this.addEventListener("loadend", fn);
      } else {
        this[kEvents].loadend = null;
      }
    }
    get onerror() {
      webidl.brandCheck(this, FileReader);
      return this[kEvents].error;
    }
    set onerror(fn) {
      webidl.brandCheck(this, FileReader);
      if (this[kEvents].error) {
        this.removeEventListener("error", this[kEvents].error);
      }
      if (typeof fn === "function") {
        this[kEvents].error = fn;
        this.addEventListener("error", fn);
      } else {
        this[kEvents].error = null;
      }
    }
    get onloadstart() {
      webidl.brandCheck(this, FileReader);
      return this[kEvents].loadstart;
    }
    set onloadstart(fn) {
      webidl.brandCheck(this, FileReader);
      if (this[kEvents].loadstart) {
        this.removeEventListener("loadstart", this[kEvents].loadstart);
      }
      if (typeof fn === "function") {
        this[kEvents].loadstart = fn;
        this.addEventListener("loadstart", fn);
      } else {
        this[kEvents].loadstart = null;
      }
    }
    get onprogress() {
      webidl.brandCheck(this, FileReader);
      return this[kEvents].progress;
    }
    set onprogress(fn) {
      webidl.brandCheck(this, FileReader);
      if (this[kEvents].progress) {
        this.removeEventListener("progress", this[kEvents].progress);
      }
      if (typeof fn === "function") {
        this[kEvents].progress = fn;
        this.addEventListener("progress", fn);
      } else {
        this[kEvents].progress = null;
      }
    }
    get onload() {
      webidl.brandCheck(this, FileReader);
      return this[kEvents].load;
    }
    set onload(fn) {
      webidl.brandCheck(this, FileReader);
      if (this[kEvents].load) {
        this.removeEventListener("load", this[kEvents].load);
      }
      if (typeof fn === "function") {
        this[kEvents].load = fn;
        this.addEventListener("load", fn);
      } else {
        this[kEvents].load = null;
      }
    }
    get onabort() {
      webidl.brandCheck(this, FileReader);
      return this[kEvents].abort;
    }
    set onabort(fn) {
      webidl.brandCheck(this, FileReader);
      if (this[kEvents].abort) {
        this.removeEventListener("abort", this[kEvents].abort);
      }
      if (typeof fn === "function") {
        this[kEvents].abort = fn;
        this.addEventListener("abort", fn);
      } else {
        this[kEvents].abort = null;
      }
    }
  }
  FileReader.EMPTY = FileReader.prototype.EMPTY = 0;
  FileReader.LOADING = FileReader.prototype.LOADING = 1;
  FileReader.DONE = FileReader.prototype.DONE = 2;
  Object.defineProperties(FileReader.prototype, {
    EMPTY: staticPropertyDescriptors,
    LOADING: staticPropertyDescriptors,
    DONE: staticPropertyDescriptors,
    readAsArrayBuffer: kEnumerableProperty,
    readAsBinaryString: kEnumerableProperty,
    readAsText: kEnumerableProperty,
    readAsDataURL: kEnumerableProperty,
    abort: kEnumerableProperty,
    readyState: kEnumerableProperty,
    result: kEnumerableProperty,
    error: kEnumerableProperty,
    onloadstart: kEnumerableProperty,
    onprogress: kEnumerableProperty,
    onload: kEnumerableProperty,
    onabort: kEnumerableProperty,
    onerror: kEnumerableProperty,
    onloadend: kEnumerableProperty,
    [Symbol.toStringTag]: {
      value: "FileReader",
      writable: false,
      enumerable: false,
      configurable: true
    }
  });
  Object.defineProperties(FileReader, {
    EMPTY: staticPropertyDescriptors,
    LOADING: staticPropertyDescriptors,
    DONE: staticPropertyDescriptors
  });
  module.exports = {
    FileReader
  };
});

// node_modules/undici/lib/web/cache/symbols.js
var require_symbols4 = __commonJS((exports, module) => {
  module.exports = {
    kConstruct: require_symbols().kConstruct
  };
});

// node_modules/undici/lib/web/cache/util.js
var require_util5 = __commonJS((exports, module) => {
  var assert = __require("assert");
  var { URLSerializer } = require_data_url();
  var { isValidHeaderName } = require_util2();
  function urlEquals(A, B, excludeFragment = false) {
    const serializedA = URLSerializer(A, excludeFragment);
    const serializedB = URLSerializer(B, excludeFragment);
    return serializedA === serializedB;
  }
  function getFieldValues(header) {
    assert(header !== null);
    const values = [];
    for (let value of header.split(",")) {
      value = value.trim();
      if (isValidHeaderName(value)) {
        values.push(value);
      }
    }
    return values;
  }
  module.exports = {
    urlEquals,
    getFieldValues
  };
});

// node_modules/undici/lib/web/cache/cache.js
var require_cache = __commonJS((exports, module) => {
  var { kConstruct } = require_symbols4();
  var { urlEquals, getFieldValues } = require_util5();
  var { kEnumerableProperty, isDisturbed } = require_util();
  var { webidl } = require_webidl();
  var { Response: Response2, cloneResponse, fromInnerResponse } = require_response();
  var { Request: Request2, fromInnerRequest } = require_request2();
  var { kState } = require_symbols2();
  var { fetching } = require_fetch();
  var { urlIsHttpHttpsScheme, createDeferredPromise, readAllBytes } = require_util2();
  var assert = __require("assert");

  class Cache {
    #relevantRequestResponseList;
    constructor() {
      if (arguments[0] !== kConstruct) {
        webidl.illegalConstructor();
      }
      webidl.util.markAsUncloneable(this);
      this.#relevantRequestResponseList = arguments[1];
    }
    async match(request, options = {}) {
      webidl.brandCheck(this, Cache);
      const prefix = "Cache.match";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      request = webidl.converters.RequestInfo(request, prefix, "request");
      options = webidl.converters.CacheQueryOptions(options, prefix, "options");
      const p = this.#internalMatchAll(request, options, 1);
      if (p.length === 0) {
        return;
      }
      return p[0];
    }
    async matchAll(request = undefined, options = {}) {
      webidl.brandCheck(this, Cache);
      const prefix = "Cache.matchAll";
      if (request !== undefined)
        request = webidl.converters.RequestInfo(request, prefix, "request");
      options = webidl.converters.CacheQueryOptions(options, prefix, "options");
      return this.#internalMatchAll(request, options);
    }
    async add(request) {
      webidl.brandCheck(this, Cache);
      const prefix = "Cache.add";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      request = webidl.converters.RequestInfo(request, prefix, "request");
      const requests = [request];
      const responseArrayPromise = this.addAll(requests);
      return await responseArrayPromise;
    }
    async addAll(requests) {
      webidl.brandCheck(this, Cache);
      const prefix = "Cache.addAll";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      const responsePromises = [];
      const requestList = [];
      for (let request of requests) {
        if (request === undefined) {
          throw webidl.errors.conversionFailed({
            prefix,
            argument: "Argument 1",
            types: ["undefined is not allowed"]
          });
        }
        request = webidl.converters.RequestInfo(request);
        if (typeof request === "string") {
          continue;
        }
        const r = request[kState];
        if (!urlIsHttpHttpsScheme(r.url) || r.method !== "GET") {
          throw webidl.errors.exception({
            header: prefix,
            message: "Expected http/s scheme when method is not GET."
          });
        }
      }
      const fetchControllers = [];
      for (const request of requests) {
        const r = new Request2(request)[kState];
        if (!urlIsHttpHttpsScheme(r.url)) {
          throw webidl.errors.exception({
            header: prefix,
            message: "Expected http/s scheme."
          });
        }
        r.initiator = "fetch";
        r.destination = "subresource";
        requestList.push(r);
        const responsePromise = createDeferredPromise();
        fetchControllers.push(fetching({
          request: r,
          processResponse(response) {
            if (response.type === "error" || response.status === 206 || response.status < 200 || response.status > 299) {
              responsePromise.reject(webidl.errors.exception({
                header: "Cache.addAll",
                message: "Received an invalid status code or the request failed."
              }));
            } else if (response.headersList.contains("vary")) {
              const fieldValues = getFieldValues(response.headersList.get("vary"));
              for (const fieldValue of fieldValues) {
                if (fieldValue === "*") {
                  responsePromise.reject(webidl.errors.exception({
                    header: "Cache.addAll",
                    message: "invalid vary field value"
                  }));
                  for (const controller of fetchControllers) {
                    controller.abort();
                  }
                  return;
                }
              }
            }
          },
          processResponseEndOfBody(response) {
            if (response.aborted) {
              responsePromise.reject(new DOMException("aborted", "AbortError"));
              return;
            }
            responsePromise.resolve(response);
          }
        }));
        responsePromises.push(responsePromise.promise);
      }
      const p = Promise.all(responsePromises);
      const responses = await p;
      const operations = [];
      let index = 0;
      for (const response of responses) {
        const operation = {
          type: "put",
          request: requestList[index],
          response
        };
        operations.push(operation);
        index++;
      }
      const cacheJobPromise = createDeferredPromise();
      let errorData = null;
      try {
        this.#batchCacheOperations(operations);
      } catch (e) {
        errorData = e;
      }
      queueMicrotask(() => {
        if (errorData === null) {
          cacheJobPromise.resolve(undefined);
        } else {
          cacheJobPromise.reject(errorData);
        }
      });
      return cacheJobPromise.promise;
    }
    async put(request, response) {
      webidl.brandCheck(this, Cache);
      const prefix = "Cache.put";
      webidl.argumentLengthCheck(arguments, 2, prefix);
      request = webidl.converters.RequestInfo(request, prefix, "request");
      response = webidl.converters.Response(response, prefix, "response");
      let innerRequest = null;
      if (request instanceof Request2) {
        innerRequest = request[kState];
      } else {
        innerRequest = new Request2(request)[kState];
      }
      if (!urlIsHttpHttpsScheme(innerRequest.url) || innerRequest.method !== "GET") {
        throw webidl.errors.exception({
          header: prefix,
          message: "Expected an http/s scheme when method is not GET"
        });
      }
      const innerResponse = response[kState];
      if (innerResponse.status === 206) {
        throw webidl.errors.exception({
          header: prefix,
          message: "Got 206 status"
        });
      }
      if (innerResponse.headersList.contains("vary")) {
        const fieldValues = getFieldValues(innerResponse.headersList.get("vary"));
        for (const fieldValue of fieldValues) {
          if (fieldValue === "*") {
            throw webidl.errors.exception({
              header: prefix,
              message: "Got * vary field value"
            });
          }
        }
      }
      if (innerResponse.body && (isDisturbed(innerResponse.body.stream) || innerResponse.body.stream.locked)) {
        throw webidl.errors.exception({
          header: prefix,
          message: "Response body is locked or disturbed"
        });
      }
      const clonedResponse = cloneResponse(innerResponse);
      const bodyReadPromise = createDeferredPromise();
      if (innerResponse.body != null) {
        const stream = innerResponse.body.stream;
        const reader = stream.getReader();
        readAllBytes(reader).then(bodyReadPromise.resolve, bodyReadPromise.reject);
      } else {
        bodyReadPromise.resolve(undefined);
      }
      const operations = [];
      const operation = {
        type: "put",
        request: innerRequest,
        response: clonedResponse
      };
      operations.push(operation);
      const bytes = await bodyReadPromise.promise;
      if (clonedResponse.body != null) {
        clonedResponse.body.source = bytes;
      }
      const cacheJobPromise = createDeferredPromise();
      let errorData = null;
      try {
        this.#batchCacheOperations(operations);
      } catch (e) {
        errorData = e;
      }
      queueMicrotask(() => {
        if (errorData === null) {
          cacheJobPromise.resolve();
        } else {
          cacheJobPromise.reject(errorData);
        }
      });
      return cacheJobPromise.promise;
    }
    async delete(request, options = {}) {
      webidl.brandCheck(this, Cache);
      const prefix = "Cache.delete";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      request = webidl.converters.RequestInfo(request, prefix, "request");
      options = webidl.converters.CacheQueryOptions(options, prefix, "options");
      let r = null;
      if (request instanceof Request2) {
        r = request[kState];
        if (r.method !== "GET" && !options.ignoreMethod) {
          return false;
        }
      } else {
        assert(typeof request === "string");
        r = new Request2(request)[kState];
      }
      const operations = [];
      const operation = {
        type: "delete",
        request: r,
        options
      };
      operations.push(operation);
      const cacheJobPromise = createDeferredPromise();
      let errorData = null;
      let requestResponses;
      try {
        requestResponses = this.#batchCacheOperations(operations);
      } catch (e) {
        errorData = e;
      }
      queueMicrotask(() => {
        if (errorData === null) {
          cacheJobPromise.resolve(!!requestResponses?.length);
        } else {
          cacheJobPromise.reject(errorData);
        }
      });
      return cacheJobPromise.promise;
    }
    async keys(request = undefined, options = {}) {
      webidl.brandCheck(this, Cache);
      const prefix = "Cache.keys";
      if (request !== undefined)
        request = webidl.converters.RequestInfo(request, prefix, "request");
      options = webidl.converters.CacheQueryOptions(options, prefix, "options");
      let r = null;
      if (request !== undefined) {
        if (request instanceof Request2) {
          r = request[kState];
          if (r.method !== "GET" && !options.ignoreMethod) {
            return [];
          }
        } else if (typeof request === "string") {
          r = new Request2(request)[kState];
        }
      }
      const promise = createDeferredPromise();
      const requests = [];
      if (request === undefined) {
        for (const requestResponse of this.#relevantRequestResponseList) {
          requests.push(requestResponse[0]);
        }
      } else {
        const requestResponses = this.#queryCache(r, options);
        for (const requestResponse of requestResponses) {
          requests.push(requestResponse[0]);
        }
      }
      queueMicrotask(() => {
        const requestList = [];
        for (const request2 of requests) {
          const requestObject = fromInnerRequest(request2, new AbortController().signal, "immutable");
          requestList.push(requestObject);
        }
        promise.resolve(Object.freeze(requestList));
      });
      return promise.promise;
    }
    #batchCacheOperations(operations) {
      const cache = this.#relevantRequestResponseList;
      const backupCache = [...cache];
      const addedItems = [];
      const resultList = [];
      try {
        for (const operation of operations) {
          if (operation.type !== "delete" && operation.type !== "put") {
            throw webidl.errors.exception({
              header: "Cache.#batchCacheOperations",
              message: 'operation type does not match "delete" or "put"'
            });
          }
          if (operation.type === "delete" && operation.response != null) {
            throw webidl.errors.exception({
              header: "Cache.#batchCacheOperations",
              message: "delete operation should not have an associated response"
            });
          }
          if (this.#queryCache(operation.request, operation.options, addedItems).length) {
            throw new DOMException("???", "InvalidStateError");
          }
          let requestResponses;
          if (operation.type === "delete") {
            requestResponses = this.#queryCache(operation.request, operation.options);
            if (requestResponses.length === 0) {
              return [];
            }
            for (const requestResponse of requestResponses) {
              const idx = cache.indexOf(requestResponse);
              assert(idx !== -1);
              cache.splice(idx, 1);
            }
          } else if (operation.type === "put") {
            if (operation.response == null) {
              throw webidl.errors.exception({
                header: "Cache.#batchCacheOperations",
                message: "put operation should have an associated response"
              });
            }
            const r = operation.request;
            if (!urlIsHttpHttpsScheme(r.url)) {
              throw webidl.errors.exception({
                header: "Cache.#batchCacheOperations",
                message: "expected http or https scheme"
              });
            }
            if (r.method !== "GET") {
              throw webidl.errors.exception({
                header: "Cache.#batchCacheOperations",
                message: "not get method"
              });
            }
            if (operation.options != null) {
              throw webidl.errors.exception({
                header: "Cache.#batchCacheOperations",
                message: "options must not be defined"
              });
            }
            requestResponses = this.#queryCache(operation.request);
            for (const requestResponse of requestResponses) {
              const idx = cache.indexOf(requestResponse);
              assert(idx !== -1);
              cache.splice(idx, 1);
            }
            cache.push([operation.request, operation.response]);
            addedItems.push([operation.request, operation.response]);
          }
          resultList.push([operation.request, operation.response]);
        }
        return resultList;
      } catch (e) {
        this.#relevantRequestResponseList.length = 0;
        this.#relevantRequestResponseList = backupCache;
        throw e;
      }
    }
    #queryCache(requestQuery, options, targetStorage) {
      const resultList = [];
      const storage = targetStorage ?? this.#relevantRequestResponseList;
      for (const requestResponse of storage) {
        const [cachedRequest, cachedResponse] = requestResponse;
        if (this.#requestMatchesCachedItem(requestQuery, cachedRequest, cachedResponse, options)) {
          resultList.push(requestResponse);
        }
      }
      return resultList;
    }
    #requestMatchesCachedItem(requestQuery, request, response = null, options) {
      const queryURL = new URL(requestQuery.url);
      const cachedURL = new URL(request.url);
      if (options?.ignoreSearch) {
        cachedURL.search = "";
        queryURL.search = "";
      }
      if (!urlEquals(queryURL, cachedURL, true)) {
        return false;
      }
      if (response == null || options?.ignoreVary || !response.headersList.contains("vary")) {
        return true;
      }
      const fieldValues = getFieldValues(response.headersList.get("vary"));
      for (const fieldValue of fieldValues) {
        if (fieldValue === "*") {
          return false;
        }
        const requestValue = request.headersList.get(fieldValue);
        const queryValue = requestQuery.headersList.get(fieldValue);
        if (requestValue !== queryValue) {
          return false;
        }
      }
      return true;
    }
    #internalMatchAll(request, options, maxResponses = Infinity) {
      let r = null;
      if (request !== undefined) {
        if (request instanceof Request2) {
          r = request[kState];
          if (r.method !== "GET" && !options.ignoreMethod) {
            return [];
          }
        } else if (typeof request === "string") {
          r = new Request2(request)[kState];
        }
      }
      const responses = [];
      if (request === undefined) {
        for (const requestResponse of this.#relevantRequestResponseList) {
          responses.push(requestResponse[1]);
        }
      } else {
        const requestResponses = this.#queryCache(r, options);
        for (const requestResponse of requestResponses) {
          responses.push(requestResponse[1]);
        }
      }
      const responseList = [];
      for (const response of responses) {
        const responseObject = fromInnerResponse(response, "immutable");
        responseList.push(responseObject.clone());
        if (responseList.length >= maxResponses) {
          break;
        }
      }
      return Object.freeze(responseList);
    }
  }
  Object.defineProperties(Cache.prototype, {
    [Symbol.toStringTag]: {
      value: "Cache",
      configurable: true
    },
    match: kEnumerableProperty,
    matchAll: kEnumerableProperty,
    add: kEnumerableProperty,
    addAll: kEnumerableProperty,
    put: kEnumerableProperty,
    delete: kEnumerableProperty,
    keys: kEnumerableProperty
  });
  var cacheQueryOptionConverters = [
    {
      key: "ignoreSearch",
      converter: webidl.converters.boolean,
      defaultValue: () => false
    },
    {
      key: "ignoreMethod",
      converter: webidl.converters.boolean,
      defaultValue: () => false
    },
    {
      key: "ignoreVary",
      converter: webidl.converters.boolean,
      defaultValue: () => false
    }
  ];
  webidl.converters.CacheQueryOptions = webidl.dictionaryConverter(cacheQueryOptionConverters);
  webidl.converters.MultiCacheQueryOptions = webidl.dictionaryConverter([
    ...cacheQueryOptionConverters,
    {
      key: "cacheName",
      converter: webidl.converters.DOMString
    }
  ]);
  webidl.converters.Response = webidl.interfaceConverter(Response2);
  webidl.converters["sequence<RequestInfo>"] = webidl.sequenceConverter(webidl.converters.RequestInfo);
  module.exports = {
    Cache
  };
});

// node_modules/undici/lib/web/cache/cachestorage.js
var require_cachestorage = __commonJS((exports, module) => {
  var { kConstruct } = require_symbols4();
  var { Cache } = require_cache();
  var { webidl } = require_webidl();
  var { kEnumerableProperty } = require_util();

  class CacheStorage {
    #caches = new Map;
    constructor() {
      if (arguments[0] !== kConstruct) {
        webidl.illegalConstructor();
      }
      webidl.util.markAsUncloneable(this);
    }
    async match(request, options = {}) {
      webidl.brandCheck(this, CacheStorage);
      webidl.argumentLengthCheck(arguments, 1, "CacheStorage.match");
      request = webidl.converters.RequestInfo(request);
      options = webidl.converters.MultiCacheQueryOptions(options);
      if (options.cacheName != null) {
        if (this.#caches.has(options.cacheName)) {
          const cacheList = this.#caches.get(options.cacheName);
          const cache = new Cache(kConstruct, cacheList);
          return await cache.match(request, options);
        }
      } else {
        for (const cacheList of this.#caches.values()) {
          const cache = new Cache(kConstruct, cacheList);
          const response = await cache.match(request, options);
          if (response !== undefined) {
            return response;
          }
        }
      }
    }
    async has(cacheName) {
      webidl.brandCheck(this, CacheStorage);
      const prefix = "CacheStorage.has";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      cacheName = webidl.converters.DOMString(cacheName, prefix, "cacheName");
      return this.#caches.has(cacheName);
    }
    async open(cacheName) {
      webidl.brandCheck(this, CacheStorage);
      const prefix = "CacheStorage.open";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      cacheName = webidl.converters.DOMString(cacheName, prefix, "cacheName");
      if (this.#caches.has(cacheName)) {
        const cache2 = this.#caches.get(cacheName);
        return new Cache(kConstruct, cache2);
      }
      const cache = [];
      this.#caches.set(cacheName, cache);
      return new Cache(kConstruct, cache);
    }
    async delete(cacheName) {
      webidl.brandCheck(this, CacheStorage);
      const prefix = "CacheStorage.delete";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      cacheName = webidl.converters.DOMString(cacheName, prefix, "cacheName");
      return this.#caches.delete(cacheName);
    }
    async keys() {
      webidl.brandCheck(this, CacheStorage);
      const keys = this.#caches.keys();
      return [...keys];
    }
  }
  Object.defineProperties(CacheStorage.prototype, {
    [Symbol.toStringTag]: {
      value: "CacheStorage",
      configurable: true
    },
    match: kEnumerableProperty,
    has: kEnumerableProperty,
    open: kEnumerableProperty,
    delete: kEnumerableProperty,
    keys: kEnumerableProperty
  });
  module.exports = {
    CacheStorage
  };
});

// node_modules/undici/lib/web/cookies/constants.js
var require_constants4 = __commonJS((exports, module) => {
  var maxAttributeValueSize = 1024;
  var maxNameValuePairSize = 4096;
  module.exports = {
    maxAttributeValueSize,
    maxNameValuePairSize
  };
});

// node_modules/undici/lib/web/cookies/util.js
var require_util6 = __commonJS((exports, module) => {
  function isCTLExcludingHtab(value) {
    for (let i = 0;i < value.length; ++i) {
      const code = value.charCodeAt(i);
      if (code >= 0 && code <= 8 || code >= 10 && code <= 31 || code === 127) {
        return true;
      }
    }
    return false;
  }
  function validateCookieName(name) {
    for (let i = 0;i < name.length; ++i) {
      const code = name.charCodeAt(i);
      if (code < 33 || code > 126 || code === 34 || code === 40 || code === 41 || code === 60 || code === 62 || code === 64 || code === 44 || code === 59 || code === 58 || code === 92 || code === 47 || code === 91 || code === 93 || code === 63 || code === 61 || code === 123 || code === 125) {
        throw new Error("Invalid cookie name");
      }
    }
  }
  function validateCookieValue(value) {
    let len = value.length;
    let i = 0;
    if (value[0] === '"') {
      if (len === 1 || value[len - 1] !== '"') {
        throw new Error("Invalid cookie value");
      }
      --len;
      ++i;
    }
    while (i < len) {
      const code = value.charCodeAt(i++);
      if (code < 33 || code > 126 || code === 34 || code === 44 || code === 59 || code === 92) {
        throw new Error("Invalid cookie value");
      }
    }
  }
  function validateCookiePath(path) {
    for (let i = 0;i < path.length; ++i) {
      const code = path.charCodeAt(i);
      if (code < 32 || code === 127 || code === 59) {
        throw new Error("Invalid cookie path");
      }
    }
  }
  function validateCookieDomain(domain) {
    if (domain.startsWith("-") || domain.endsWith(".") || domain.endsWith("-")) {
      throw new Error("Invalid cookie domain");
    }
  }
  var IMFDays = [
    "Sun",
    "Mon",
    "Tue",
    "Wed",
    "Thu",
    "Fri",
    "Sat"
  ];
  var IMFMonths = [
    "Jan",
    "Feb",
    "Mar",
    "Apr",
    "May",
    "Jun",
    "Jul",
    "Aug",
    "Sep",
    "Oct",
    "Nov",
    "Dec"
  ];
  var IMFPaddedNumbers = Array(61).fill(0).map((_, i) => i.toString().padStart(2, "0"));
  function toIMFDate(date) {
    if (typeof date === "number") {
      date = new Date(date);
    }
    return `${IMFDays[date.getUTCDay()]}, ${IMFPaddedNumbers[date.getUTCDate()]} ${IMFMonths[date.getUTCMonth()]} ${date.getUTCFullYear()} ${IMFPaddedNumbers[date.getUTCHours()]}:${IMFPaddedNumbers[date.getUTCMinutes()]}:${IMFPaddedNumbers[date.getUTCSeconds()]} GMT`;
  }
  function validateCookieMaxAge(maxAge) {
    if (maxAge < 0) {
      throw new Error("Invalid cookie max-age");
    }
  }
  function stringify(cookie) {
    if (cookie.name.length === 0) {
      return null;
    }
    validateCookieName(cookie.name);
    validateCookieValue(cookie.value);
    const out = [`${cookie.name}=${cookie.value}`];
    if (cookie.name.startsWith("__Secure-")) {
      cookie.secure = true;
    }
    if (cookie.name.startsWith("__Host-")) {
      cookie.secure = true;
      cookie.domain = null;
      cookie.path = "/";
    }
    if (cookie.secure) {
      out.push("Secure");
    }
    if (cookie.httpOnly) {
      out.push("HttpOnly");
    }
    if (typeof cookie.maxAge === "number") {
      validateCookieMaxAge(cookie.maxAge);
      out.push(`Max-Age=${cookie.maxAge}`);
    }
    if (cookie.domain) {
      validateCookieDomain(cookie.domain);
      out.push(`Domain=${cookie.domain}`);
    }
    if (cookie.path) {
      validateCookiePath(cookie.path);
      out.push(`Path=${cookie.path}`);
    }
    if (cookie.expires && cookie.expires.toString() !== "Invalid Date") {
      out.push(`Expires=${toIMFDate(cookie.expires)}`);
    }
    if (cookie.sameSite) {
      out.push(`SameSite=${cookie.sameSite}`);
    }
    for (const part of cookie.unparsed) {
      if (!part.includes("=")) {
        throw new Error("Invalid unparsed");
      }
      const [key, ...value] = part.split("=");
      out.push(`${key.trim()}=${value.join("=")}`);
    }
    return out.join("; ");
  }
  module.exports = {
    isCTLExcludingHtab,
    validateCookieName,
    validateCookiePath,
    validateCookieValue,
    toIMFDate,
    stringify
  };
});

// node_modules/undici/lib/web/cookies/parse.js
var require_parse = __commonJS((exports, module) => {
  var { maxNameValuePairSize, maxAttributeValueSize } = require_constants4();
  var { isCTLExcludingHtab } = require_util6();
  var { collectASequenceOfCodePointsFast } = require_data_url();
  var assert = __require("assert");
  function parseSetCookie(header) {
    if (isCTLExcludingHtab(header)) {
      return null;
    }
    let nameValuePair = "";
    let unparsedAttributes = "";
    let name = "";
    let value = "";
    if (header.includes(";")) {
      const position = { position: 0 };
      nameValuePair = collectASequenceOfCodePointsFast(";", header, position);
      unparsedAttributes = header.slice(position.position);
    } else {
      nameValuePair = header;
    }
    if (!nameValuePair.includes("=")) {
      value = nameValuePair;
    } else {
      const position = { position: 0 };
      name = collectASequenceOfCodePointsFast("=", nameValuePair, position);
      value = nameValuePair.slice(position.position + 1);
    }
    name = name.trim();
    value = value.trim();
    if (name.length + value.length > maxNameValuePairSize) {
      return null;
    }
    return {
      name,
      value,
      ...parseUnparsedAttributes(unparsedAttributes)
    };
  }
  function parseUnparsedAttributes(unparsedAttributes, cookieAttributeList = {}) {
    if (unparsedAttributes.length === 0) {
      return cookieAttributeList;
    }
    assert(unparsedAttributes[0] === ";");
    unparsedAttributes = unparsedAttributes.slice(1);
    let cookieAv = "";
    if (unparsedAttributes.includes(";")) {
      cookieAv = collectASequenceOfCodePointsFast(";", unparsedAttributes, { position: 0 });
      unparsedAttributes = unparsedAttributes.slice(cookieAv.length);
    } else {
      cookieAv = unparsedAttributes;
      unparsedAttributes = "";
    }
    let attributeName = "";
    let attributeValue = "";
    if (cookieAv.includes("=")) {
      const position = { position: 0 };
      attributeName = collectASequenceOfCodePointsFast("=", cookieAv, position);
      attributeValue = cookieAv.slice(position.position + 1);
    } else {
      attributeName = cookieAv;
    }
    attributeName = attributeName.trim();
    attributeValue = attributeValue.trim();
    if (attributeValue.length > maxAttributeValueSize) {
      return parseUnparsedAttributes(unparsedAttributes, cookieAttributeList);
    }
    const attributeNameLowercase = attributeName.toLowerCase();
    if (attributeNameLowercase === "expires") {
      const expiryTime = new Date(attributeValue);
      cookieAttributeList.expires = expiryTime;
    } else if (attributeNameLowercase === "max-age") {
      const charCode = attributeValue.charCodeAt(0);
      if ((charCode < 48 || charCode > 57) && attributeValue[0] !== "-") {
        return parseUnparsedAttributes(unparsedAttributes, cookieAttributeList);
      }
      if (!/^\d+$/.test(attributeValue)) {
        return parseUnparsedAttributes(unparsedAttributes, cookieAttributeList);
      }
      const deltaSeconds = Number(attributeValue);
      cookieAttributeList.maxAge = deltaSeconds;
    } else if (attributeNameLowercase === "domain") {
      let cookieDomain = attributeValue;
      if (cookieDomain[0] === ".") {
        cookieDomain = cookieDomain.slice(1);
      }
      cookieDomain = cookieDomain.toLowerCase();
      cookieAttributeList.domain = cookieDomain;
    } else if (attributeNameLowercase === "path") {
      let cookiePath = "";
      if (attributeValue.length === 0 || attributeValue[0] !== "/") {
        cookiePath = "/";
      } else {
        cookiePath = attributeValue;
      }
      cookieAttributeList.path = cookiePath;
    } else if (attributeNameLowercase === "secure") {
      cookieAttributeList.secure = true;
    } else if (attributeNameLowercase === "httponly") {
      cookieAttributeList.httpOnly = true;
    } else if (attributeNameLowercase === "samesite") {
      let enforcement = "Default";
      const attributeValueLowercase = attributeValue.toLowerCase();
      if (attributeValueLowercase.includes("none")) {
        enforcement = "None";
      }
      if (attributeValueLowercase.includes("strict")) {
        enforcement = "Strict";
      }
      if (attributeValueLowercase.includes("lax")) {
        enforcement = "Lax";
      }
      cookieAttributeList.sameSite = enforcement;
    } else {
      cookieAttributeList.unparsed ??= [];
      cookieAttributeList.unparsed.push(`${attributeName}=${attributeValue}`);
    }
    return parseUnparsedAttributes(unparsedAttributes, cookieAttributeList);
  }
  module.exports = {
    parseSetCookie,
    parseUnparsedAttributes
  };
});

// node_modules/undici/lib/web/cookies/index.js
var require_cookies = __commonJS((exports, module) => {
  var { parseSetCookie } = require_parse();
  var { stringify } = require_util6();
  var { webidl } = require_webidl();
  var { Headers: Headers2 } = require_headers();
  function getCookies(headers) {
    webidl.argumentLengthCheck(arguments, 1, "getCookies");
    webidl.brandCheck(headers, Headers2, { strict: false });
    const cookie = headers.get("cookie");
    const out = {};
    if (!cookie) {
      return out;
    }
    for (const piece of cookie.split(";")) {
      const [name, ...value] = piece.split("=");
      out[name.trim()] = value.join("=");
    }
    return out;
  }
  function deleteCookie(headers, name, attributes) {
    webidl.brandCheck(headers, Headers2, { strict: false });
    const prefix = "deleteCookie";
    webidl.argumentLengthCheck(arguments, 2, prefix);
    name = webidl.converters.DOMString(name, prefix, "name");
    attributes = webidl.converters.DeleteCookieAttributes(attributes);
    setCookie(headers, {
      name,
      value: "",
      expires: new Date(0),
      ...attributes
    });
  }
  function getSetCookies(headers) {
    webidl.argumentLengthCheck(arguments, 1, "getSetCookies");
    webidl.brandCheck(headers, Headers2, { strict: false });
    const cookies = headers.getSetCookie();
    if (!cookies) {
      return [];
    }
    return cookies.map((pair) => parseSetCookie(pair));
  }
  function setCookie(headers, cookie) {
    webidl.argumentLengthCheck(arguments, 2, "setCookie");
    webidl.brandCheck(headers, Headers2, { strict: false });
    cookie = webidl.converters.Cookie(cookie);
    const str = stringify(cookie);
    if (str) {
      headers.append("Set-Cookie", str);
    }
  }
  webidl.converters.DeleteCookieAttributes = webidl.dictionaryConverter([
    {
      converter: webidl.nullableConverter(webidl.converters.DOMString),
      key: "path",
      defaultValue: () => null
    },
    {
      converter: webidl.nullableConverter(webidl.converters.DOMString),
      key: "domain",
      defaultValue: () => null
    }
  ]);
  webidl.converters.Cookie = webidl.dictionaryConverter([
    {
      converter: webidl.converters.DOMString,
      key: "name"
    },
    {
      converter: webidl.converters.DOMString,
      key: "value"
    },
    {
      converter: webidl.nullableConverter((value) => {
        if (typeof value === "number") {
          return webidl.converters["unsigned long long"](value);
        }
        return new Date(value);
      }),
      key: "expires",
      defaultValue: () => null
    },
    {
      converter: webidl.nullableConverter(webidl.converters["long long"]),
      key: "maxAge",
      defaultValue: () => null
    },
    {
      converter: webidl.nullableConverter(webidl.converters.DOMString),
      key: "domain",
      defaultValue: () => null
    },
    {
      converter: webidl.nullableConverter(webidl.converters.DOMString),
      key: "path",
      defaultValue: () => null
    },
    {
      converter: webidl.nullableConverter(webidl.converters.boolean),
      key: "secure",
      defaultValue: () => null
    },
    {
      converter: webidl.nullableConverter(webidl.converters.boolean),
      key: "httpOnly",
      defaultValue: () => null
    },
    {
      converter: webidl.converters.USVString,
      key: "sameSite",
      allowedValues: ["Strict", "Lax", "None"]
    },
    {
      converter: webidl.sequenceConverter(webidl.converters.DOMString),
      key: "unparsed",
      defaultValue: () => new Array(0)
    }
  ]);
  module.exports = {
    getCookies,
    deleteCookie,
    getSetCookies,
    setCookie
  };
});

// node_modules/undici/lib/web/websocket/events.js
var require_events = __commonJS((exports, module) => {
  var { webidl } = require_webidl();
  var { kEnumerableProperty } = require_util();
  var { kConstruct } = require_symbols();
  var { MessagePort } = __require("worker_threads");

  class MessageEvent extends Event {
    #eventInit;
    constructor(type, eventInitDict = {}) {
      if (type === kConstruct) {
        super(arguments[1], arguments[2]);
        webidl.util.markAsUncloneable(this);
        return;
      }
      const prefix = "MessageEvent constructor";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      type = webidl.converters.DOMString(type, prefix, "type");
      eventInitDict = webidl.converters.MessageEventInit(eventInitDict, prefix, "eventInitDict");
      super(type, eventInitDict);
      this.#eventInit = eventInitDict;
      webidl.util.markAsUncloneable(this);
    }
    get data() {
      webidl.brandCheck(this, MessageEvent);
      return this.#eventInit.data;
    }
    get origin() {
      webidl.brandCheck(this, MessageEvent);
      return this.#eventInit.origin;
    }
    get lastEventId() {
      webidl.brandCheck(this, MessageEvent);
      return this.#eventInit.lastEventId;
    }
    get source() {
      webidl.brandCheck(this, MessageEvent);
      return this.#eventInit.source;
    }
    get ports() {
      webidl.brandCheck(this, MessageEvent);
      if (!Object.isFrozen(this.#eventInit.ports)) {
        Object.freeze(this.#eventInit.ports);
      }
      return this.#eventInit.ports;
    }
    initMessageEvent(type, bubbles = false, cancelable = false, data = null, origin = "", lastEventId = "", source = null, ports = []) {
      webidl.brandCheck(this, MessageEvent);
      webidl.argumentLengthCheck(arguments, 1, "MessageEvent.initMessageEvent");
      return new MessageEvent(type, {
        bubbles,
        cancelable,
        data,
        origin,
        lastEventId,
        source,
        ports
      });
    }
    static createFastMessageEvent(type, init) {
      const messageEvent = new MessageEvent(kConstruct, type, init);
      messageEvent.#eventInit = init;
      messageEvent.#eventInit.data ??= null;
      messageEvent.#eventInit.origin ??= "";
      messageEvent.#eventInit.lastEventId ??= "";
      messageEvent.#eventInit.source ??= null;
      messageEvent.#eventInit.ports ??= [];
      return messageEvent;
    }
  }
  var { createFastMessageEvent } = MessageEvent;
  delete MessageEvent.createFastMessageEvent;

  class CloseEvent extends Event {
    #eventInit;
    constructor(type, eventInitDict = {}) {
      const prefix = "CloseEvent constructor";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      type = webidl.converters.DOMString(type, prefix, "type");
      eventInitDict = webidl.converters.CloseEventInit(eventInitDict);
      super(type, eventInitDict);
      this.#eventInit = eventInitDict;
      webidl.util.markAsUncloneable(this);
    }
    get wasClean() {
      webidl.brandCheck(this, CloseEvent);
      return this.#eventInit.wasClean;
    }
    get code() {
      webidl.brandCheck(this, CloseEvent);
      return this.#eventInit.code;
    }
    get reason() {
      webidl.brandCheck(this, CloseEvent);
      return this.#eventInit.reason;
    }
  }

  class ErrorEvent extends Event {
    #eventInit;
    constructor(type, eventInitDict) {
      const prefix = "ErrorEvent constructor";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      super(type, eventInitDict);
      webidl.util.markAsUncloneable(this);
      type = webidl.converters.DOMString(type, prefix, "type");
      eventInitDict = webidl.converters.ErrorEventInit(eventInitDict ?? {});
      this.#eventInit = eventInitDict;
    }
    get message() {
      webidl.brandCheck(this, ErrorEvent);
      return this.#eventInit.message;
    }
    get filename() {
      webidl.brandCheck(this, ErrorEvent);
      return this.#eventInit.filename;
    }
    get lineno() {
      webidl.brandCheck(this, ErrorEvent);
      return this.#eventInit.lineno;
    }
    get colno() {
      webidl.brandCheck(this, ErrorEvent);
      return this.#eventInit.colno;
    }
    get error() {
      webidl.brandCheck(this, ErrorEvent);
      return this.#eventInit.error;
    }
  }
  Object.defineProperties(MessageEvent.prototype, {
    [Symbol.toStringTag]: {
      value: "MessageEvent",
      configurable: true
    },
    data: kEnumerableProperty,
    origin: kEnumerableProperty,
    lastEventId: kEnumerableProperty,
    source: kEnumerableProperty,
    ports: kEnumerableProperty,
    initMessageEvent: kEnumerableProperty
  });
  Object.defineProperties(CloseEvent.prototype, {
    [Symbol.toStringTag]: {
      value: "CloseEvent",
      configurable: true
    },
    reason: kEnumerableProperty,
    code: kEnumerableProperty,
    wasClean: kEnumerableProperty
  });
  Object.defineProperties(ErrorEvent.prototype, {
    [Symbol.toStringTag]: {
      value: "ErrorEvent",
      configurable: true
    },
    message: kEnumerableProperty,
    filename: kEnumerableProperty,
    lineno: kEnumerableProperty,
    colno: kEnumerableProperty,
    error: kEnumerableProperty
  });
  webidl.converters.MessagePort = webidl.interfaceConverter(MessagePort);
  webidl.converters["sequence<MessagePort>"] = webidl.sequenceConverter(webidl.converters.MessagePort);
  var eventInit = [
    {
      key: "bubbles",
      converter: webidl.converters.boolean,
      defaultValue: () => false
    },
    {
      key: "cancelable",
      converter: webidl.converters.boolean,
      defaultValue: () => false
    },
    {
      key: "composed",
      converter: webidl.converters.boolean,
      defaultValue: () => false
    }
  ];
  webidl.converters.MessageEventInit = webidl.dictionaryConverter([
    ...eventInit,
    {
      key: "data",
      converter: webidl.converters.any,
      defaultValue: () => null
    },
    {
      key: "origin",
      converter: webidl.converters.USVString,
      defaultValue: () => ""
    },
    {
      key: "lastEventId",
      converter: webidl.converters.DOMString,
      defaultValue: () => ""
    },
    {
      key: "source",
      converter: webidl.nullableConverter(webidl.converters.MessagePort),
      defaultValue: () => null
    },
    {
      key: "ports",
      converter: webidl.converters["sequence<MessagePort>"],
      defaultValue: () => new Array(0)
    }
  ]);
  webidl.converters.CloseEventInit = webidl.dictionaryConverter([
    ...eventInit,
    {
      key: "wasClean",
      converter: webidl.converters.boolean,
      defaultValue: () => false
    },
    {
      key: "code",
      converter: webidl.converters["unsigned short"],
      defaultValue: () => 0
    },
    {
      key: "reason",
      converter: webidl.converters.USVString,
      defaultValue: () => ""
    }
  ]);
  webidl.converters.ErrorEventInit = webidl.dictionaryConverter([
    ...eventInit,
    {
      key: "message",
      converter: webidl.converters.DOMString,
      defaultValue: () => ""
    },
    {
      key: "filename",
      converter: webidl.converters.USVString,
      defaultValue: () => ""
    },
    {
      key: "lineno",
      converter: webidl.converters["unsigned long"],
      defaultValue: () => 0
    },
    {
      key: "colno",
      converter: webidl.converters["unsigned long"],
      defaultValue: () => 0
    },
    {
      key: "error",
      converter: webidl.converters.any
    }
  ]);
  module.exports = {
    MessageEvent,
    CloseEvent,
    ErrorEvent,
    createFastMessageEvent
  };
});

// node_modules/undici/lib/web/websocket/constants.js
var require_constants5 = __commonJS((exports, module) => {
  var uid = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11";
  var staticPropertyDescriptors = {
    enumerable: true,
    writable: false,
    configurable: false
  };
  var states2 = {
    CONNECTING: 0,
    OPEN: 1,
    CLOSING: 2,
    CLOSED: 3
  };
  var sentCloseFrameState = {
    NOT_SENT: 0,
    PROCESSING: 1,
    SENT: 2
  };
  var opcodes = {
    CONTINUATION: 0,
    TEXT: 1,
    BINARY: 2,
    CLOSE: 8,
    PING: 9,
    PONG: 10
  };
  var maxUnsigned16Bit = 2 ** 16 - 1;
  var parserStates = {
    INFO: 0,
    PAYLOADLENGTH_16: 2,
    PAYLOADLENGTH_64: 3,
    READ_DATA: 4
  };
  var emptyBuffer = Buffer.allocUnsafe(0);
  var sendHints = {
    string: 1,
    typedArray: 2,
    arrayBuffer: 3,
    blob: 4
  };
  module.exports = {
    uid,
    sentCloseFrameState,
    staticPropertyDescriptors,
    states: states2,
    opcodes,
    maxUnsigned16Bit,
    parserStates,
    emptyBuffer,
    sendHints
  };
});

// node_modules/undici/lib/web/websocket/symbols.js
var require_symbols5 = __commonJS((exports, module) => {
  module.exports = {
    kWebSocketURL: Symbol("url"),
    kReadyState: Symbol("ready state"),
    kController: Symbol("controller"),
    kResponse: Symbol("response"),
    kBinaryType: Symbol("binary type"),
    kSentClose: Symbol("sent close"),
    kReceivedClose: Symbol("received close"),
    kByteParser: Symbol("byte parser")
  };
});

// node_modules/undici/lib/web/websocket/util.js
var require_util7 = __commonJS((exports, module) => {
  var { kReadyState, kController, kResponse, kBinaryType, kWebSocketURL } = require_symbols5();
  var { states: states2, opcodes } = require_constants5();
  var { ErrorEvent, createFastMessageEvent } = require_events();
  var { isUtf8 } = __require("buffer");
  var { collectASequenceOfCodePointsFast, removeHTTPWhitespace } = require_data_url();
  function isConnecting(ws) {
    return ws[kReadyState] === states2.CONNECTING;
  }
  function isEstablished(ws) {
    return ws[kReadyState] === states2.OPEN;
  }
  function isClosing(ws) {
    return ws[kReadyState] === states2.CLOSING;
  }
  function isClosed(ws) {
    return ws[kReadyState] === states2.CLOSED;
  }
  function fireEvent(e, target, eventFactory = (type, init) => new Event(type, init), eventInitDict = {}) {
    const event = eventFactory(e, eventInitDict);
    target.dispatchEvent(event);
  }
  function websocketMessageReceived(ws, type, data) {
    if (ws[kReadyState] !== states2.OPEN) {
      return;
    }
    let dataForEvent;
    if (type === opcodes.TEXT) {
      try {
        dataForEvent = utf8Decode(data);
      } catch {
        failWebsocketConnection(ws, "Received invalid UTF-8 in text frame.");
        return;
      }
    } else if (type === opcodes.BINARY) {
      if (ws[kBinaryType] === "blob") {
        dataForEvent = new Blob([data]);
      } else {
        dataForEvent = toArrayBuffer(data);
      }
    }
    fireEvent("message", ws, createFastMessageEvent, {
      origin: ws[kWebSocketURL].origin,
      data: dataForEvent
    });
  }
  function toArrayBuffer(buffer) {
    if (buffer.byteLength === buffer.buffer.byteLength) {
      return buffer.buffer;
    }
    return buffer.buffer.slice(buffer.byteOffset, buffer.byteOffset + buffer.byteLength);
  }
  function isValidSubprotocol(protocol) {
    if (protocol.length === 0) {
      return false;
    }
    for (let i = 0;i < protocol.length; ++i) {
      const code = protocol.charCodeAt(i);
      if (code < 33 || code > 126 || code === 34 || code === 40 || code === 41 || code === 44 || code === 47 || code === 58 || code === 59 || code === 60 || code === 61 || code === 62 || code === 63 || code === 64 || code === 91 || code === 92 || code === 93 || code === 123 || code === 125) {
        return false;
      }
    }
    return true;
  }
  function isValidStatusCode(code) {
    if (code >= 1000 && code < 1015) {
      return code !== 1004 && code !== 1005 && code !== 1006;
    }
    return code >= 3000 && code <= 4999;
  }
  function failWebsocketConnection(ws, reason) {
    const { [kController]: controller, [kResponse]: response } = ws;
    controller.abort();
    if (response?.socket && !response.socket.destroyed) {
      response.socket.destroy();
    }
    if (reason) {
      fireEvent("error", ws, (type, init) => new ErrorEvent(type, init), {
        error: new Error(reason),
        message: reason
      });
    }
  }
  function isControlFrame(opcode) {
    return opcode === opcodes.CLOSE || opcode === opcodes.PING || opcode === opcodes.PONG;
  }
  function isContinuationFrame(opcode) {
    return opcode === opcodes.CONTINUATION;
  }
  function isTextBinaryFrame(opcode) {
    return opcode === opcodes.TEXT || opcode === opcodes.BINARY;
  }
  function isValidOpcode(opcode) {
    return isTextBinaryFrame(opcode) || isContinuationFrame(opcode) || isControlFrame(opcode);
  }
  function parseExtensions(extensions) {
    const position = { position: 0 };
    const extensionList = new Map;
    while (position.position < extensions.length) {
      const pair = collectASequenceOfCodePointsFast(";", extensions, position);
      const [name, value = ""] = pair.split("=");
      extensionList.set(removeHTTPWhitespace(name, true, false), removeHTTPWhitespace(value, false, true));
      position.position++;
    }
    return extensionList;
  }
  function isValidClientWindowBits(value) {
    if (value.length === 0) {
      return false;
    }
    for (let i = 0;i < value.length; i++) {
      const byte = value.charCodeAt(i);
      if (byte < 48 || byte > 57) {
        return false;
      }
    }
    const num = Number.parseInt(value, 10);
    return num >= 8 && num <= 15;
  }
  var hasIntl = typeof process.versions.icu === "string";
  var fatalDecoder = hasIntl ? new TextDecoder("utf-8", { fatal: true }) : undefined;
  var utf8Decode = hasIntl ? fatalDecoder.decode.bind(fatalDecoder) : function(buffer) {
    if (isUtf8(buffer)) {
      return buffer.toString("utf-8");
    }
    throw new TypeError("Invalid utf-8 received.");
  };
  module.exports = {
    isConnecting,
    isEstablished,
    isClosing,
    isClosed,
    fireEvent,
    isValidSubprotocol,
    isValidStatusCode,
    failWebsocketConnection,
    websocketMessageReceived,
    utf8Decode,
    isControlFrame,
    isContinuationFrame,
    isTextBinaryFrame,
    isValidOpcode,
    parseExtensions,
    isValidClientWindowBits
  };
});

// node_modules/undici/lib/web/websocket/frame.js
var require_frame = __commonJS((exports, module) => {
  var { maxUnsigned16Bit } = require_constants5();
  var BUFFER_SIZE = 16386;
  var crypto2;
  var buffer = null;
  var bufIdx = BUFFER_SIZE;
  try {
    crypto2 = __require("crypto");
  } catch {
    crypto2 = {
      randomFillSync: function randomFillSync(buffer2, _offset, _size) {
        for (let i = 0;i < buffer2.length; ++i) {
          buffer2[i] = Math.random() * 255 | 0;
        }
        return buffer2;
      }
    };
  }
  function generateMask() {
    if (bufIdx === BUFFER_SIZE) {
      bufIdx = 0;
      crypto2.randomFillSync(buffer ??= Buffer.allocUnsafe(BUFFER_SIZE), 0, BUFFER_SIZE);
    }
    return [buffer[bufIdx++], buffer[bufIdx++], buffer[bufIdx++], buffer[bufIdx++]];
  }

  class WebsocketFrameSend {
    constructor(data) {
      this.frameData = data;
    }
    createFrame(opcode) {
      const frameData = this.frameData;
      const maskKey = generateMask();
      const bodyLength = frameData?.byteLength ?? 0;
      let payloadLength = bodyLength;
      let offset = 6;
      if (bodyLength > maxUnsigned16Bit) {
        offset += 8;
        payloadLength = 127;
      } else if (bodyLength > 125) {
        offset += 2;
        payloadLength = 126;
      }
      const buffer2 = Buffer.allocUnsafe(bodyLength + offset);
      buffer2[0] = buffer2[1] = 0;
      buffer2[0] |= 128;
      buffer2[0] = (buffer2[0] & 240) + opcode;
      /*! ws. MIT License. Einar Otto Stangvik <einaros@gmail.com> */
      buffer2[offset - 4] = maskKey[0];
      buffer2[offset - 3] = maskKey[1];
      buffer2[offset - 2] = maskKey[2];
      buffer2[offset - 1] = maskKey[3];
      buffer2[1] = payloadLength;
      if (payloadLength === 126) {
        buffer2.writeUInt16BE(bodyLength, 2);
      } else if (payloadLength === 127) {
        buffer2[2] = buffer2[3] = 0;
        buffer2.writeUIntBE(bodyLength, 4, 6);
      }
      buffer2[1] |= 128;
      for (let i = 0;i < bodyLength; ++i) {
        buffer2[offset + i] = frameData[i] ^ maskKey[i & 3];
      }
      return buffer2;
    }
  }
  module.exports = {
    WebsocketFrameSend
  };
});

// node_modules/undici/lib/web/websocket/connection.js
var require_connection = __commonJS((exports, module) => {
  var { uid, states: states2, sentCloseFrameState, emptyBuffer, opcodes } = require_constants5();
  var {
    kReadyState,
    kSentClose,
    kByteParser,
    kReceivedClose,
    kResponse
  } = require_symbols5();
  var { fireEvent, failWebsocketConnection, isClosing, isClosed, isEstablished, parseExtensions } = require_util7();
  var { channels } = require_diagnostics();
  var { CloseEvent } = require_events();
  var { makeRequest } = require_request2();
  var { fetching } = require_fetch();
  var { Headers: Headers2, getHeadersList } = require_headers();
  var { getDecodeSplit } = require_util2();
  var { WebsocketFrameSend } = require_frame();
  var crypto2;
  try {
    crypto2 = __require("crypto");
  } catch {}
  function establishWebSocketConnection(url, protocols, client, ws, onEstablish, options) {
    const requestURL = url;
    requestURL.protocol = url.protocol === "ws:" ? "http:" : "https:";
    const request = makeRequest({
      urlList: [requestURL],
      client,
      serviceWorkers: "none",
      referrer: "no-referrer",
      mode: "websocket",
      credentials: "include",
      cache: "no-store",
      redirect: "error"
    });
    if (options.headers) {
      const headersList = getHeadersList(new Headers2(options.headers));
      request.headersList = headersList;
    }
    const keyValue = crypto2.randomBytes(16).toString("base64");
    request.headersList.append("sec-websocket-key", keyValue);
    request.headersList.append("sec-websocket-version", "13");
    for (const protocol of protocols) {
      request.headersList.append("sec-websocket-protocol", protocol);
    }
    const permessageDeflate = "permessage-deflate; client_max_window_bits";
    request.headersList.append("sec-websocket-extensions", permessageDeflate);
    const controller = fetching({
      request,
      useParallelQueue: true,
      dispatcher: options.dispatcher,
      processResponse(response) {
        if (response.type === "error" || response.status !== 101) {
          failWebsocketConnection(ws, "Received network error or non-101 status code.");
          return;
        }
        if (protocols.length !== 0 && !response.headersList.get("Sec-WebSocket-Protocol")) {
          failWebsocketConnection(ws, "Server did not respond with sent protocols.");
          return;
        }
        if (response.headersList.get("Upgrade")?.toLowerCase() !== "websocket") {
          failWebsocketConnection(ws, 'Server did not set Upgrade header to "websocket".');
          return;
        }
        if (response.headersList.get("Connection")?.toLowerCase() !== "upgrade") {
          failWebsocketConnection(ws, 'Server did not set Connection header to "upgrade".');
          return;
        }
        const secWSAccept = response.headersList.get("Sec-WebSocket-Accept");
        const digest = crypto2.createHash("sha1").update(keyValue + uid).digest("base64");
        if (secWSAccept !== digest) {
          failWebsocketConnection(ws, "Incorrect hash received in Sec-WebSocket-Accept header.");
          return;
        }
        const secExtension = response.headersList.get("Sec-WebSocket-Extensions");
        let extensions;
        if (secExtension !== null) {
          extensions = parseExtensions(secExtension);
          if (!extensions.has("permessage-deflate")) {
            failWebsocketConnection(ws, "Sec-WebSocket-Extensions header does not match.");
            return;
          }
        }
        const secProtocol = response.headersList.get("Sec-WebSocket-Protocol");
        if (secProtocol !== null) {
          const requestProtocols = getDecodeSplit("sec-websocket-protocol", request.headersList);
          if (!requestProtocols.includes(secProtocol)) {
            failWebsocketConnection(ws, "Protocol was not set in the opening handshake.");
            return;
          }
        }
        response.socket.on("data", onSocketData);
        response.socket.on("close", onSocketClose);
        response.socket.on("error", onSocketError);
        if (channels.open.hasSubscribers) {
          channels.open.publish({
            address: response.socket.address(),
            protocol: secProtocol,
            extensions: secExtension
          });
        }
        onEstablish(response, extensions);
      }
    });
    return controller;
  }
  function closeWebSocketConnection(ws, code, reason, reasonByteLength) {
    if (isClosing(ws) || isClosed(ws)) {} else if (!isEstablished(ws)) {
      failWebsocketConnection(ws, "Connection was closed before it was established.");
      ws[kReadyState] = states2.CLOSING;
    } else if (ws[kSentClose] === sentCloseFrameState.NOT_SENT) {
      ws[kSentClose] = sentCloseFrameState.PROCESSING;
      const frame = new WebsocketFrameSend;
      if (code !== undefined && reason === undefined) {
        frame.frameData = Buffer.allocUnsafe(2);
        frame.frameData.writeUInt16BE(code, 0);
      } else if (code !== undefined && reason !== undefined) {
        frame.frameData = Buffer.allocUnsafe(2 + reasonByteLength);
        frame.frameData.writeUInt16BE(code, 0);
        frame.frameData.write(reason, 2, "utf-8");
      } else {
        frame.frameData = emptyBuffer;
      }
      const socket = ws[kResponse].socket;
      socket.write(frame.createFrame(opcodes.CLOSE));
      ws[kSentClose] = sentCloseFrameState.SENT;
      ws[kReadyState] = states2.CLOSING;
    } else {
      ws[kReadyState] = states2.CLOSING;
    }
  }
  function onSocketData(chunk) {
    if (!this.ws[kByteParser].write(chunk)) {
      this.pause();
    }
  }
  function onSocketClose() {
    const { ws } = this;
    const { [kResponse]: response } = ws;
    response.socket.off("data", onSocketData);
    response.socket.off("close", onSocketClose);
    response.socket.off("error", onSocketError);
    const wasClean = ws[kSentClose] === sentCloseFrameState.SENT && ws[kReceivedClose];
    let code = 1005;
    let reason = "";
    const result = ws[kByteParser].closingInfo;
    if (result && !result.error) {
      code = result.code ?? 1005;
      reason = result.reason;
    } else if (!ws[kReceivedClose]) {
      code = 1006;
    }
    ws[kReadyState] = states2.CLOSED;
    fireEvent("close", ws, (type, init) => new CloseEvent(type, init), {
      wasClean,
      code,
      reason
    });
    if (channels.close.hasSubscribers) {
      channels.close.publish({
        websocket: ws,
        code,
        reason
      });
    }
  }
  function onSocketError(error) {
    const { ws } = this;
    ws[kReadyState] = states2.CLOSING;
    if (channels.socketError.hasSubscribers) {
      channels.socketError.publish(error);
    }
    this.destroy();
  }
  module.exports = {
    establishWebSocketConnection,
    closeWebSocketConnection
  };
});

// node_modules/undici/lib/web/websocket/permessage-deflate.js
var require_permessage_deflate = __commonJS((exports, module) => {
  var { createInflateRaw, Z_DEFAULT_WINDOWBITS } = __require("zlib");
  var { isValidClientWindowBits } = require_util7();
  var { MessageSizeExceededError } = require_errors();
  var tail = Buffer.from([0, 0, 255, 255]);
  var kBuffer = Symbol("kBuffer");
  var kLength = Symbol("kLength");
  var kDefaultMaxDecompressedSize = 4 * 1024 * 1024;

  class PerMessageDeflate {
    #inflate;
    #options = {};
    #aborted = false;
    #currentCallback = null;
    constructor(extensions) {
      this.#options.serverNoContextTakeover = extensions.has("server_no_context_takeover");
      this.#options.serverMaxWindowBits = extensions.get("server_max_window_bits");
    }
    decompress(chunk, fin, callback) {
      if (this.#aborted) {
        callback(new MessageSizeExceededError);
        return;
      }
      if (!this.#inflate) {
        let windowBits = Z_DEFAULT_WINDOWBITS;
        if (this.#options.serverMaxWindowBits) {
          if (!isValidClientWindowBits(this.#options.serverMaxWindowBits)) {
            callback(new Error("Invalid server_max_window_bits"));
            return;
          }
          windowBits = Number.parseInt(this.#options.serverMaxWindowBits);
        }
        try {
          this.#inflate = createInflateRaw({ windowBits });
        } catch (err) {
          callback(err);
          return;
        }
        this.#inflate[kBuffer] = [];
        this.#inflate[kLength] = 0;
        this.#inflate.on("data", (data) => {
          if (this.#aborted) {
            return;
          }
          this.#inflate[kLength] += data.length;
          if (this.#inflate[kLength] > kDefaultMaxDecompressedSize) {
            this.#aborted = true;
            this.#inflate.removeAllListeners();
            this.#inflate.destroy();
            this.#inflate = null;
            if (this.#currentCallback) {
              const cb = this.#currentCallback;
              this.#currentCallback = null;
              cb(new MessageSizeExceededError);
            }
            return;
          }
          this.#inflate[kBuffer].push(data);
        });
        this.#inflate.on("error", (err) => {
          this.#inflate = null;
          callback(err);
        });
      }
      this.#currentCallback = callback;
      this.#inflate.write(chunk);
      if (fin) {
        this.#inflate.write(tail);
      }
      this.#inflate.flush(() => {
        if (this.#aborted || !this.#inflate) {
          return;
        }
        const full = Buffer.concat(this.#inflate[kBuffer], this.#inflate[kLength]);
        this.#inflate[kBuffer].length = 0;
        this.#inflate[kLength] = 0;
        this.#currentCallback = null;
        callback(null, full);
      });
    }
  }
  module.exports = { PerMessageDeflate };
});

// node_modules/undici/lib/web/websocket/receiver.js
var require_receiver = __commonJS((exports, module) => {
  var { Writable } = __require("stream");
  var assert = __require("assert");
  var { parserStates, opcodes, states: states2, emptyBuffer, sentCloseFrameState } = require_constants5();
  var { kReadyState, kSentClose, kResponse, kReceivedClose } = require_symbols5();
  var { channels } = require_diagnostics();
  var {
    isValidStatusCode,
    isValidOpcode,
    failWebsocketConnection,
    websocketMessageReceived,
    utf8Decode,
    isControlFrame,
    isTextBinaryFrame,
    isContinuationFrame
  } = require_util7();
  var { WebsocketFrameSend } = require_frame();
  var { closeWebSocketConnection } = require_connection();
  var { PerMessageDeflate } = require_permessage_deflate();

  class ByteParser extends Writable {
    #buffers = [];
    #byteOffset = 0;
    #loop = false;
    #state = parserStates.INFO;
    #info = {};
    #fragments = [];
    #extensions;
    constructor(ws, extensions) {
      super();
      this.ws = ws;
      this.#extensions = extensions == null ? new Map : extensions;
      if (this.#extensions.has("permessage-deflate")) {
        this.#extensions.set("permessage-deflate", new PerMessageDeflate(extensions));
      }
    }
    _write(chunk, _, callback) {
      this.#buffers.push(chunk);
      this.#byteOffset += chunk.length;
      this.#loop = true;
      this.run(callback);
    }
    run(callback) {
      while (this.#loop) {
        if (this.#state === parserStates.INFO) {
          if (this.#byteOffset < 2) {
            return callback();
          }
          const buffer = this.consume(2);
          const fin = (buffer[0] & 128) !== 0;
          const opcode = buffer[0] & 15;
          const masked = (buffer[1] & 128) === 128;
          const fragmented = !fin && opcode !== opcodes.CONTINUATION;
          const payloadLength = buffer[1] & 127;
          const rsv1 = buffer[0] & 64;
          const rsv2 = buffer[0] & 32;
          const rsv3 = buffer[0] & 16;
          if (!isValidOpcode(opcode)) {
            failWebsocketConnection(this.ws, "Invalid opcode received");
            return callback();
          }
          if (masked) {
            failWebsocketConnection(this.ws, "Frame cannot be masked");
            return callback();
          }
          if (rsv1 !== 0 && !this.#extensions.has("permessage-deflate")) {
            failWebsocketConnection(this.ws, "Expected RSV1 to be clear.");
            return;
          }
          if (rsv2 !== 0 || rsv3 !== 0) {
            failWebsocketConnection(this.ws, "RSV1, RSV2, RSV3 must be clear");
            return;
          }
          if (fragmented && !isTextBinaryFrame(opcode)) {
            failWebsocketConnection(this.ws, "Invalid frame type was fragmented.");
            return;
          }
          if (isTextBinaryFrame(opcode) && this.#fragments.length > 0) {
            failWebsocketConnection(this.ws, "Expected continuation frame");
            return;
          }
          if (this.#info.fragmented && fragmented) {
            failWebsocketConnection(this.ws, "Fragmented frame exceeded 125 bytes.");
            return;
          }
          if ((payloadLength > 125 || fragmented) && isControlFrame(opcode)) {
            failWebsocketConnection(this.ws, "Control frame either too large or fragmented");
            return;
          }
          if (isContinuationFrame(opcode) && this.#fragments.length === 0 && !this.#info.compressed) {
            failWebsocketConnection(this.ws, "Unexpected continuation frame");
            return;
          }
          if (payloadLength <= 125) {
            this.#info.payloadLength = payloadLength;
            this.#state = parserStates.READ_DATA;
          } else if (payloadLength === 126) {
            this.#state = parserStates.PAYLOADLENGTH_16;
          } else if (payloadLength === 127) {
            this.#state = parserStates.PAYLOADLENGTH_64;
          }
          if (isTextBinaryFrame(opcode)) {
            this.#info.binaryType = opcode;
            this.#info.compressed = rsv1 !== 0;
          }
          this.#info.opcode = opcode;
          this.#info.masked = masked;
          this.#info.fin = fin;
          this.#info.fragmented = fragmented;
        } else if (this.#state === parserStates.PAYLOADLENGTH_16) {
          if (this.#byteOffset < 2) {
            return callback();
          }
          const buffer = this.consume(2);
          this.#info.payloadLength = buffer.readUInt16BE(0);
          this.#state = parserStates.READ_DATA;
        } else if (this.#state === parserStates.PAYLOADLENGTH_64) {
          if (this.#byteOffset < 8) {
            return callback();
          }
          const buffer = this.consume(8);
          const upper = buffer.readUInt32BE(0);
          const lower = buffer.readUInt32BE(4);
          if (upper !== 0 || lower > 2 ** 31 - 1) {
            failWebsocketConnection(this.ws, "Received payload length > 2^31 bytes.");
            return;
          }
          this.#info.payloadLength = lower;
          this.#state = parserStates.READ_DATA;
        } else if (this.#state === parserStates.READ_DATA) {
          if (this.#byteOffset < this.#info.payloadLength) {
            return callback();
          }
          const body = this.consume(this.#info.payloadLength);
          if (isControlFrame(this.#info.opcode)) {
            this.#loop = this.parseControlFrame(body);
            this.#state = parserStates.INFO;
          } else {
            if (!this.#info.compressed) {
              this.#fragments.push(body);
              if (!this.#info.fragmented && this.#info.fin) {
                const fullMessage = Buffer.concat(this.#fragments);
                websocketMessageReceived(this.ws, this.#info.binaryType, fullMessage);
                this.#fragments.length = 0;
              }
              this.#state = parserStates.INFO;
            } else {
              this.#extensions.get("permessage-deflate").decompress(body, this.#info.fin, (error, data) => {
                if (error) {
                  failWebsocketConnection(this.ws, error.message);
                  return;
                }
                this.#fragments.push(data);
                if (!this.#info.fin) {
                  this.#state = parserStates.INFO;
                  this.#loop = true;
                  this.run(callback);
                  return;
                }
                websocketMessageReceived(this.ws, this.#info.binaryType, Buffer.concat(this.#fragments));
                this.#loop = true;
                this.#state = parserStates.INFO;
                this.#fragments.length = 0;
                this.run(callback);
              });
              this.#loop = false;
              break;
            }
          }
        }
      }
    }
    consume(n) {
      if (n > this.#byteOffset) {
        throw new Error("Called consume() before buffers satiated.");
      } else if (n === 0) {
        return emptyBuffer;
      }
      if (this.#buffers[0].length === n) {
        this.#byteOffset -= this.#buffers[0].length;
        return this.#buffers.shift();
      }
      const buffer = Buffer.allocUnsafe(n);
      let offset = 0;
      while (offset !== n) {
        const next = this.#buffers[0];
        const { length } = next;
        if (length + offset === n) {
          buffer.set(this.#buffers.shift(), offset);
          break;
        } else if (length + offset > n) {
          buffer.set(next.subarray(0, n - offset), offset);
          this.#buffers[0] = next.subarray(n - offset);
          break;
        } else {
          buffer.set(this.#buffers.shift(), offset);
          offset += next.length;
        }
      }
      this.#byteOffset -= n;
      return buffer;
    }
    parseCloseBody(data) {
      assert(data.length !== 1);
      let code;
      if (data.length >= 2) {
        code = data.readUInt16BE(0);
      }
      if (code !== undefined && !isValidStatusCode(code)) {
        return { code: 1002, reason: "Invalid status code", error: true };
      }
      let reason = data.subarray(2);
      if (reason[0] === 239 && reason[1] === 187 && reason[2] === 191) {
        reason = reason.subarray(3);
      }
      try {
        reason = utf8Decode(reason);
      } catch {
        return { code: 1007, reason: "Invalid UTF-8", error: true };
      }
      return { code, reason, error: false };
    }
    parseControlFrame(body) {
      const { opcode, payloadLength } = this.#info;
      if (opcode === opcodes.CLOSE) {
        if (payloadLength === 1) {
          failWebsocketConnection(this.ws, "Received close frame with a 1-byte body.");
          return false;
        }
        this.#info.closeInfo = this.parseCloseBody(body);
        if (this.#info.closeInfo.error) {
          const { code, reason } = this.#info.closeInfo;
          closeWebSocketConnection(this.ws, code, reason, reason.length);
          failWebsocketConnection(this.ws, reason);
          return false;
        }
        if (this.ws[kSentClose] !== sentCloseFrameState.SENT) {
          let body2 = emptyBuffer;
          if (this.#info.closeInfo.code) {
            body2 = Buffer.allocUnsafe(2);
            body2.writeUInt16BE(this.#info.closeInfo.code, 0);
          }
          const closeFrame = new WebsocketFrameSend(body2);
          this.ws[kResponse].socket.write(closeFrame.createFrame(opcodes.CLOSE), (err) => {
            if (!err) {
              this.ws[kSentClose] = sentCloseFrameState.SENT;
            }
          });
        }
        this.ws[kReadyState] = states2.CLOSING;
        this.ws[kReceivedClose] = true;
        return false;
      } else if (opcode === opcodes.PING) {
        if (!this.ws[kReceivedClose]) {
          const frame = new WebsocketFrameSend(body);
          this.ws[kResponse].socket.write(frame.createFrame(opcodes.PONG));
          if (channels.ping.hasSubscribers) {
            channels.ping.publish({
              payload: body
            });
          }
        }
      } else if (opcode === opcodes.PONG) {
        if (channels.pong.hasSubscribers) {
          channels.pong.publish({
            payload: body
          });
        }
      }
      return true;
    }
    get closingInfo() {
      return this.#info.closeInfo;
    }
  }
  module.exports = {
    ByteParser
  };
});

// node_modules/undici/lib/web/websocket/sender.js
var require_sender = __commonJS((exports, module) => {
  var { WebsocketFrameSend } = require_frame();
  var { opcodes, sendHints } = require_constants5();
  var FixedQueue = require_fixed_queue();
  var FastBuffer = Buffer[Symbol.species];

  class SendQueue {
    #queue = new FixedQueue;
    #running = false;
    #socket;
    constructor(socket) {
      this.#socket = socket;
    }
    add(item, cb, hint) {
      if (hint !== sendHints.blob) {
        const frame = createFrame(item, hint);
        if (!this.#running) {
          this.#socket.write(frame, cb);
        } else {
          const node2 = {
            promise: null,
            callback: cb,
            frame
          };
          this.#queue.push(node2);
        }
        return;
      }
      const node = {
        promise: item.arrayBuffer().then((ab) => {
          node.promise = null;
          node.frame = createFrame(ab, hint);
        }),
        callback: cb,
        frame: null
      };
      this.#queue.push(node);
      if (!this.#running) {
        this.#run();
      }
    }
    async#run() {
      this.#running = true;
      const queue = this.#queue;
      while (!queue.isEmpty()) {
        const node = queue.shift();
        if (node.promise !== null) {
          await node.promise;
        }
        this.#socket.write(node.frame, node.callback);
        node.callback = node.frame = null;
      }
      this.#running = false;
    }
  }
  function createFrame(data, hint) {
    return new WebsocketFrameSend(toBuffer(data, hint)).createFrame(hint === sendHints.string ? opcodes.TEXT : opcodes.BINARY);
  }
  function toBuffer(data, hint) {
    switch (hint) {
      case sendHints.string:
        return Buffer.from(data);
      case sendHints.arrayBuffer:
      case sendHints.blob:
        return new FastBuffer(data);
      case sendHints.typedArray:
        return new FastBuffer(data.buffer, data.byteOffset, data.byteLength);
    }
  }
  module.exports = { SendQueue };
});

// node_modules/undici/lib/web/websocket/websocket.js
var require_websocket = __commonJS((exports, module) => {
  var { webidl } = require_webidl();
  var { URLSerializer } = require_data_url();
  var { environmentSettingsObject } = require_util2();
  var { staticPropertyDescriptors, states: states2, sentCloseFrameState, sendHints } = require_constants5();
  var {
    kWebSocketURL,
    kReadyState,
    kController,
    kBinaryType,
    kResponse,
    kSentClose,
    kByteParser
  } = require_symbols5();
  var {
    isConnecting,
    isEstablished,
    isClosing,
    isValidSubprotocol,
    fireEvent
  } = require_util7();
  var { establishWebSocketConnection, closeWebSocketConnection } = require_connection();
  var { ByteParser } = require_receiver();
  var { kEnumerableProperty, isBlobLike } = require_util();
  var { getGlobalDispatcher } = require_global2();
  var { types } = __require("util");
  var { ErrorEvent, CloseEvent } = require_events();
  var { SendQueue } = require_sender();

  class WebSocket extends EventTarget {
    #events = {
      open: null,
      error: null,
      close: null,
      message: null
    };
    #bufferedAmount = 0;
    #protocol = "";
    #extensions = "";
    #sendQueue;
    constructor(url, protocols = []) {
      super();
      webidl.util.markAsUncloneable(this);
      const prefix = "WebSocket constructor";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      const options = webidl.converters["DOMString or sequence<DOMString> or WebSocketInit"](protocols, prefix, "options");
      url = webidl.converters.USVString(url, prefix, "url");
      protocols = options.protocols;
      const baseURL = environmentSettingsObject.settingsObject.baseUrl;
      let urlRecord;
      try {
        urlRecord = new URL(url, baseURL);
      } catch (e) {
        throw new DOMException(e, "SyntaxError");
      }
      if (urlRecord.protocol === "http:") {
        urlRecord.protocol = "ws:";
      } else if (urlRecord.protocol === "https:") {
        urlRecord.protocol = "wss:";
      }
      if (urlRecord.protocol !== "ws:" && urlRecord.protocol !== "wss:") {
        throw new DOMException(`Expected a ws: or wss: protocol, got ${urlRecord.protocol}`, "SyntaxError");
      }
      if (urlRecord.hash || urlRecord.href.endsWith("#")) {
        throw new DOMException("Got fragment", "SyntaxError");
      }
      if (typeof protocols === "string") {
        protocols = [protocols];
      }
      if (protocols.length !== new Set(protocols.map((p) => p.toLowerCase())).size) {
        throw new DOMException("Invalid Sec-WebSocket-Protocol value", "SyntaxError");
      }
      if (protocols.length > 0 && !protocols.every((p) => isValidSubprotocol(p))) {
        throw new DOMException("Invalid Sec-WebSocket-Protocol value", "SyntaxError");
      }
      this[kWebSocketURL] = new URL(urlRecord.href);
      const client = environmentSettingsObject.settingsObject;
      this[kController] = establishWebSocketConnection(urlRecord, protocols, client, this, (response, extensions) => this.#onConnectionEstablished(response, extensions), options);
      this[kReadyState] = WebSocket.CONNECTING;
      this[kSentClose] = sentCloseFrameState.NOT_SENT;
      this[kBinaryType] = "blob";
    }
    close(code = undefined, reason = undefined) {
      webidl.brandCheck(this, WebSocket);
      const prefix = "WebSocket.close";
      if (code !== undefined) {
        code = webidl.converters["unsigned short"](code, prefix, "code", { clamp: true });
      }
      if (reason !== undefined) {
        reason = webidl.converters.USVString(reason, prefix, "reason");
      }
      if (code !== undefined) {
        if (code !== 1000 && (code < 3000 || code > 4999)) {
          throw new DOMException("invalid code", "InvalidAccessError");
        }
      }
      let reasonByteLength = 0;
      if (reason !== undefined) {
        reasonByteLength = Buffer.byteLength(reason);
        if (reasonByteLength > 123) {
          throw new DOMException(`Reason must be less than 123 bytes; received ${reasonByteLength}`, "SyntaxError");
        }
      }
      closeWebSocketConnection(this, code, reason, reasonByteLength);
    }
    send(data) {
      webidl.brandCheck(this, WebSocket);
      const prefix = "WebSocket.send";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      data = webidl.converters.WebSocketSendData(data, prefix, "data");
      if (isConnecting(this)) {
        throw new DOMException("Sent before connected.", "InvalidStateError");
      }
      if (!isEstablished(this) || isClosing(this)) {
        return;
      }
      if (typeof data === "string") {
        const length = Buffer.byteLength(data);
        this.#bufferedAmount += length;
        this.#sendQueue.add(data, () => {
          this.#bufferedAmount -= length;
        }, sendHints.string);
      } else if (types.isArrayBuffer(data)) {
        this.#bufferedAmount += data.byteLength;
        this.#sendQueue.add(data, () => {
          this.#bufferedAmount -= data.byteLength;
        }, sendHints.arrayBuffer);
      } else if (ArrayBuffer.isView(data)) {
        this.#bufferedAmount += data.byteLength;
        this.#sendQueue.add(data, () => {
          this.#bufferedAmount -= data.byteLength;
        }, sendHints.typedArray);
      } else if (isBlobLike(data)) {
        this.#bufferedAmount += data.size;
        this.#sendQueue.add(data, () => {
          this.#bufferedAmount -= data.size;
        }, sendHints.blob);
      }
    }
    get readyState() {
      webidl.brandCheck(this, WebSocket);
      return this[kReadyState];
    }
    get bufferedAmount() {
      webidl.brandCheck(this, WebSocket);
      return this.#bufferedAmount;
    }
    get url() {
      webidl.brandCheck(this, WebSocket);
      return URLSerializer(this[kWebSocketURL]);
    }
    get extensions() {
      webidl.brandCheck(this, WebSocket);
      return this.#extensions;
    }
    get protocol() {
      webidl.brandCheck(this, WebSocket);
      return this.#protocol;
    }
    get onopen() {
      webidl.brandCheck(this, WebSocket);
      return this.#events.open;
    }
    set onopen(fn) {
      webidl.brandCheck(this, WebSocket);
      if (this.#events.open) {
        this.removeEventListener("open", this.#events.open);
      }
      if (typeof fn === "function") {
        this.#events.open = fn;
        this.addEventListener("open", fn);
      } else {
        this.#events.open = null;
      }
    }
    get onerror() {
      webidl.brandCheck(this, WebSocket);
      return this.#events.error;
    }
    set onerror(fn) {
      webidl.brandCheck(this, WebSocket);
      if (this.#events.error) {
        this.removeEventListener("error", this.#events.error);
      }
      if (typeof fn === "function") {
        this.#events.error = fn;
        this.addEventListener("error", fn);
      } else {
        this.#events.error = null;
      }
    }
    get onclose() {
      webidl.brandCheck(this, WebSocket);
      return this.#events.close;
    }
    set onclose(fn) {
      webidl.brandCheck(this, WebSocket);
      if (this.#events.close) {
        this.removeEventListener("close", this.#events.close);
      }
      if (typeof fn === "function") {
        this.#events.close = fn;
        this.addEventListener("close", fn);
      } else {
        this.#events.close = null;
      }
    }
    get onmessage() {
      webidl.brandCheck(this, WebSocket);
      return this.#events.message;
    }
    set onmessage(fn) {
      webidl.brandCheck(this, WebSocket);
      if (this.#events.message) {
        this.removeEventListener("message", this.#events.message);
      }
      if (typeof fn === "function") {
        this.#events.message = fn;
        this.addEventListener("message", fn);
      } else {
        this.#events.message = null;
      }
    }
    get binaryType() {
      webidl.brandCheck(this, WebSocket);
      return this[kBinaryType];
    }
    set binaryType(type) {
      webidl.brandCheck(this, WebSocket);
      if (type !== "blob" && type !== "arraybuffer") {
        this[kBinaryType] = "blob";
      } else {
        this[kBinaryType] = type;
      }
    }
    #onConnectionEstablished(response, parsedExtensions) {
      this[kResponse] = response;
      const parser = new ByteParser(this, parsedExtensions);
      parser.on("drain", onParserDrain);
      parser.on("error", onParserError.bind(this));
      response.socket.ws = this;
      this[kByteParser] = parser;
      this.#sendQueue = new SendQueue(response.socket);
      this[kReadyState] = states2.OPEN;
      const extensions = response.headersList.get("sec-websocket-extensions");
      if (extensions !== null) {
        this.#extensions = extensions;
      }
      const protocol = response.headersList.get("sec-websocket-protocol");
      if (protocol !== null) {
        this.#protocol = protocol;
      }
      fireEvent("open", this);
    }
  }
  WebSocket.CONNECTING = WebSocket.prototype.CONNECTING = states2.CONNECTING;
  WebSocket.OPEN = WebSocket.prototype.OPEN = states2.OPEN;
  WebSocket.CLOSING = WebSocket.prototype.CLOSING = states2.CLOSING;
  WebSocket.CLOSED = WebSocket.prototype.CLOSED = states2.CLOSED;
  Object.defineProperties(WebSocket.prototype, {
    CONNECTING: staticPropertyDescriptors,
    OPEN: staticPropertyDescriptors,
    CLOSING: staticPropertyDescriptors,
    CLOSED: staticPropertyDescriptors,
    url: kEnumerableProperty,
    readyState: kEnumerableProperty,
    bufferedAmount: kEnumerableProperty,
    onopen: kEnumerableProperty,
    onerror: kEnumerableProperty,
    onclose: kEnumerableProperty,
    close: kEnumerableProperty,
    onmessage: kEnumerableProperty,
    binaryType: kEnumerableProperty,
    send: kEnumerableProperty,
    extensions: kEnumerableProperty,
    protocol: kEnumerableProperty,
    [Symbol.toStringTag]: {
      value: "WebSocket",
      writable: false,
      enumerable: false,
      configurable: true
    }
  });
  Object.defineProperties(WebSocket, {
    CONNECTING: staticPropertyDescriptors,
    OPEN: staticPropertyDescriptors,
    CLOSING: staticPropertyDescriptors,
    CLOSED: staticPropertyDescriptors
  });
  webidl.converters["sequence<DOMString>"] = webidl.sequenceConverter(webidl.converters.DOMString);
  webidl.converters["DOMString or sequence<DOMString>"] = function(V, prefix, argument) {
    if (webidl.util.Type(V) === "Object" && Symbol.iterator in V) {
      return webidl.converters["sequence<DOMString>"](V);
    }
    return webidl.converters.DOMString(V, prefix, argument);
  };
  webidl.converters.WebSocketInit = webidl.dictionaryConverter([
    {
      key: "protocols",
      converter: webidl.converters["DOMString or sequence<DOMString>"],
      defaultValue: () => new Array(0)
    },
    {
      key: "dispatcher",
      converter: webidl.converters.any,
      defaultValue: () => getGlobalDispatcher()
    },
    {
      key: "headers",
      converter: webidl.nullableConverter(webidl.converters.HeadersInit)
    }
  ]);
  webidl.converters["DOMString or sequence<DOMString> or WebSocketInit"] = function(V) {
    if (webidl.util.Type(V) === "Object" && !(Symbol.iterator in V)) {
      return webidl.converters.WebSocketInit(V);
    }
    return { protocols: webidl.converters["DOMString or sequence<DOMString>"](V) };
  };
  webidl.converters.WebSocketSendData = function(V) {
    if (webidl.util.Type(V) === "Object") {
      if (isBlobLike(V)) {
        return webidl.converters.Blob(V, { strict: false });
      }
      if (ArrayBuffer.isView(V) || types.isArrayBuffer(V)) {
        return webidl.converters.BufferSource(V);
      }
    }
    return webidl.converters.USVString(V);
  };
  function onParserDrain() {
    this.ws[kResponse].socket.resume();
  }
  function onParserError(err) {
    let message;
    let code;
    if (err instanceof CloseEvent) {
      message = err.reason;
      code = err.code;
    } else {
      message = err.message;
    }
    fireEvent("error", this, () => new ErrorEvent("error", { error: err, message }));
    closeWebSocketConnection(this, code);
  }
  module.exports = {
    WebSocket
  };
});

// node_modules/undici/lib/web/eventsource/util.js
var require_util8 = __commonJS((exports, module) => {
  function isValidLastEventId(value) {
    return value.indexOf("\x00") === -1;
  }
  function isASCIINumber(value) {
    if (value.length === 0)
      return false;
    for (let i = 0;i < value.length; i++) {
      if (value.charCodeAt(i) < 48 || value.charCodeAt(i) > 57)
        return false;
    }
    return true;
  }
  function delay(ms) {
    return new Promise((resolve3) => {
      setTimeout(resolve3, ms).unref();
    });
  }
  module.exports = {
    isValidLastEventId,
    isASCIINumber,
    delay
  };
});

// node_modules/undici/lib/web/eventsource/eventsource-stream.js
var require_eventsource_stream = __commonJS((exports, module) => {
  var { Transform } = __require("stream");
  var { isASCIINumber, isValidLastEventId } = require_util8();
  var BOM = [239, 187, 191];
  var LF = 10;
  var CR = 13;
  var COLON = 58;
  var SPACE = 32;

  class EventSourceStream extends Transform {
    state = null;
    checkBOM = true;
    crlfCheck = false;
    eventEndCheck = false;
    buffer = null;
    pos = 0;
    event = {
      data: undefined,
      event: undefined,
      id: undefined,
      retry: undefined
    };
    constructor(options = {}) {
      options.readableObjectMode = true;
      super(options);
      this.state = options.eventSourceSettings || {};
      if (options.push) {
        this.push = options.push;
      }
    }
    _transform(chunk, _encoding, callback) {
      if (chunk.length === 0) {
        callback();
        return;
      }
      if (this.buffer) {
        this.buffer = Buffer.concat([this.buffer, chunk]);
      } else {
        this.buffer = chunk;
      }
      if (this.checkBOM) {
        switch (this.buffer.length) {
          case 1:
            if (this.buffer[0] === BOM[0]) {
              callback();
              return;
            }
            this.checkBOM = false;
            callback();
            return;
          case 2:
            if (this.buffer[0] === BOM[0] && this.buffer[1] === BOM[1]) {
              callback();
              return;
            }
            this.checkBOM = false;
            break;
          case 3:
            if (this.buffer[0] === BOM[0] && this.buffer[1] === BOM[1] && this.buffer[2] === BOM[2]) {
              this.buffer = Buffer.alloc(0);
              this.checkBOM = false;
              callback();
              return;
            }
            this.checkBOM = false;
            break;
          default:
            if (this.buffer[0] === BOM[0] && this.buffer[1] === BOM[1] && this.buffer[2] === BOM[2]) {
              this.buffer = this.buffer.subarray(3);
            }
            this.checkBOM = false;
            break;
        }
      }
      while (this.pos < this.buffer.length) {
        if (this.eventEndCheck) {
          if (this.crlfCheck) {
            if (this.buffer[this.pos] === LF) {
              this.buffer = this.buffer.subarray(this.pos + 1);
              this.pos = 0;
              this.crlfCheck = false;
              continue;
            }
            this.crlfCheck = false;
          }
          if (this.buffer[this.pos] === LF || this.buffer[this.pos] === CR) {
            if (this.buffer[this.pos] === CR) {
              this.crlfCheck = true;
            }
            this.buffer = this.buffer.subarray(this.pos + 1);
            this.pos = 0;
            if (this.event.data !== undefined || this.event.event || this.event.id || this.event.retry) {
              this.processEvent(this.event);
            }
            this.clearEvent();
            continue;
          }
          this.eventEndCheck = false;
          continue;
        }
        if (this.buffer[this.pos] === LF || this.buffer[this.pos] === CR) {
          if (this.buffer[this.pos] === CR) {
            this.crlfCheck = true;
          }
          this.parseLine(this.buffer.subarray(0, this.pos), this.event);
          this.buffer = this.buffer.subarray(this.pos + 1);
          this.pos = 0;
          this.eventEndCheck = true;
          continue;
        }
        this.pos++;
      }
      callback();
    }
    parseLine(line, event) {
      if (line.length === 0) {
        return;
      }
      const colonPosition = line.indexOf(COLON);
      if (colonPosition === 0) {
        return;
      }
      let field = "";
      let value = "";
      if (colonPosition !== -1) {
        field = line.subarray(0, colonPosition).toString("utf8");
        let valueStart = colonPosition + 1;
        if (line[valueStart] === SPACE) {
          ++valueStart;
        }
        value = line.subarray(valueStart).toString("utf8");
      } else {
        field = line.toString("utf8");
        value = "";
      }
      switch (field) {
        case "data":
          if (event[field] === undefined) {
            event[field] = value;
          } else {
            event[field] += `
${value}`;
          }
          break;
        case "retry":
          if (isASCIINumber(value)) {
            event[field] = value;
          }
          break;
        case "id":
          if (isValidLastEventId(value)) {
            event[field] = value;
          }
          break;
        case "event":
          if (value.length > 0) {
            event[field] = value;
          }
          break;
      }
    }
    processEvent(event) {
      if (event.retry && isASCIINumber(event.retry)) {
        this.state.reconnectionTime = parseInt(event.retry, 10);
      }
      if (event.id && isValidLastEventId(event.id)) {
        this.state.lastEventId = event.id;
      }
      if (event.data !== undefined) {
        this.push({
          type: event.event || "message",
          options: {
            data: event.data,
            lastEventId: this.state.lastEventId,
            origin: this.state.origin
          }
        });
      }
    }
    clearEvent() {
      this.event = {
        data: undefined,
        event: undefined,
        id: undefined,
        retry: undefined
      };
    }
  }
  module.exports = {
    EventSourceStream
  };
});

// node_modules/undici/lib/web/eventsource/eventsource.js
var require_eventsource = __commonJS((exports, module) => {
  var { pipeline } = __require("stream");
  var { fetching } = require_fetch();
  var { makeRequest } = require_request2();
  var { webidl } = require_webidl();
  var { EventSourceStream } = require_eventsource_stream();
  var { parseMIMEType } = require_data_url();
  var { createFastMessageEvent } = require_events();
  var { isNetworkError } = require_response();
  var { delay } = require_util8();
  var { kEnumerableProperty } = require_util();
  var { environmentSettingsObject } = require_util2();
  var experimentalWarned = false;
  var defaultReconnectionTime = 3000;
  var CONNECTING = 0;
  var OPEN = 1;
  var CLOSED = 2;
  var ANONYMOUS = "anonymous";
  var USE_CREDENTIALS = "use-credentials";

  class EventSource extends EventTarget {
    #events = {
      open: null,
      error: null,
      message: null
    };
    #url = null;
    #withCredentials = false;
    #readyState = CONNECTING;
    #request = null;
    #controller = null;
    #dispatcher;
    #state;
    constructor(url, eventSourceInitDict = {}) {
      super();
      webidl.util.markAsUncloneable(this);
      const prefix = "EventSource constructor";
      webidl.argumentLengthCheck(arguments, 1, prefix);
      if (!experimentalWarned) {
        experimentalWarned = true;
        process.emitWarning("EventSource is experimental, expect them to change at any time.", {
          code: "UNDICI-ES"
        });
      }
      url = webidl.converters.USVString(url, prefix, "url");
      eventSourceInitDict = webidl.converters.EventSourceInitDict(eventSourceInitDict, prefix, "eventSourceInitDict");
      this.#dispatcher = eventSourceInitDict.dispatcher;
      this.#state = {
        lastEventId: "",
        reconnectionTime: defaultReconnectionTime
      };
      const settings = environmentSettingsObject;
      let urlRecord;
      try {
        urlRecord = new URL(url, settings.settingsObject.baseUrl);
        this.#state.origin = urlRecord.origin;
      } catch (e) {
        throw new DOMException(e, "SyntaxError");
      }
      this.#url = urlRecord.href;
      let corsAttributeState = ANONYMOUS;
      if (eventSourceInitDict.withCredentials) {
        corsAttributeState = USE_CREDENTIALS;
        this.#withCredentials = true;
      }
      const initRequest = {
        redirect: "follow",
        keepalive: true,
        mode: "cors",
        credentials: corsAttributeState === "anonymous" ? "same-origin" : "omit",
        referrer: "no-referrer"
      };
      initRequest.client = environmentSettingsObject.settingsObject;
      initRequest.headersList = [["accept", { name: "accept", value: "text/event-stream" }]];
      initRequest.cache = "no-store";
      initRequest.initiator = "other";
      initRequest.urlList = [new URL(this.#url)];
      this.#request = makeRequest(initRequest);
      this.#connect();
    }
    get readyState() {
      return this.#readyState;
    }
    get url() {
      return this.#url;
    }
    get withCredentials() {
      return this.#withCredentials;
    }
    #connect() {
      if (this.#readyState === CLOSED)
        return;
      this.#readyState = CONNECTING;
      const fetchParams = {
        request: this.#request,
        dispatcher: this.#dispatcher
      };
      const processEventSourceEndOfBody = (response) => {
        if (isNetworkError(response)) {
          this.dispatchEvent(new Event("error"));
          this.close();
        }
        this.#reconnect();
      };
      fetchParams.processResponseEndOfBody = processEventSourceEndOfBody;
      fetchParams.processResponse = (response) => {
        if (isNetworkError(response)) {
          if (response.aborted) {
            this.close();
            this.dispatchEvent(new Event("error"));
            return;
          } else {
            this.#reconnect();
            return;
          }
        }
        const contentType = response.headersList.get("content-type", true);
        const mimeType = contentType !== null ? parseMIMEType(contentType) : "failure";
        const contentTypeValid = mimeType !== "failure" && mimeType.essence === "text/event-stream";
        if (response.status !== 200 || contentTypeValid === false) {
          this.close();
          this.dispatchEvent(new Event("error"));
          return;
        }
        this.#readyState = OPEN;
        this.dispatchEvent(new Event("open"));
        this.#state.origin = response.urlList[response.urlList.length - 1].origin;
        const eventSourceStream = new EventSourceStream({
          eventSourceSettings: this.#state,
          push: (event) => {
            this.dispatchEvent(createFastMessageEvent(event.type, event.options));
          }
        });
        pipeline(response.body.stream, eventSourceStream, (error) => {
          if (error?.aborted === false) {
            this.close();
            this.dispatchEvent(new Event("error"));
          }
        });
      };
      this.#controller = fetching(fetchParams);
    }
    async#reconnect() {
      if (this.#readyState === CLOSED)
        return;
      this.#readyState = CONNECTING;
      this.dispatchEvent(new Event("error"));
      await delay(this.#state.reconnectionTime);
      if (this.#readyState !== CONNECTING)
        return;
      if (this.#state.lastEventId.length) {
        this.#request.headersList.set("last-event-id", this.#state.lastEventId, true);
      }
      this.#connect();
    }
    close() {
      webidl.brandCheck(this, EventSource);
      if (this.#readyState === CLOSED)
        return;
      this.#readyState = CLOSED;
      this.#controller.abort();
      this.#request = null;
    }
    get onopen() {
      return this.#events.open;
    }
    set onopen(fn) {
      if (this.#events.open) {
        this.removeEventListener("open", this.#events.open);
      }
      if (typeof fn === "function") {
        this.#events.open = fn;
        this.addEventListener("open", fn);
      } else {
        this.#events.open = null;
      }
    }
    get onmessage() {
      return this.#events.message;
    }
    set onmessage(fn) {
      if (this.#events.message) {
        this.removeEventListener("message", this.#events.message);
      }
      if (typeof fn === "function") {
        this.#events.message = fn;
        this.addEventListener("message", fn);
      } else {
        this.#events.message = null;
      }
    }
    get onerror() {
      return this.#events.error;
    }
    set onerror(fn) {
      if (this.#events.error) {
        this.removeEventListener("error", this.#events.error);
      }
      if (typeof fn === "function") {
        this.#events.error = fn;
        this.addEventListener("error", fn);
      } else {
        this.#events.error = null;
      }
    }
  }
  var constantsPropertyDescriptors = {
    CONNECTING: {
      __proto__: null,
      configurable: false,
      enumerable: true,
      value: CONNECTING,
      writable: false
    },
    OPEN: {
      __proto__: null,
      configurable: false,
      enumerable: true,
      value: OPEN,
      writable: false
    },
    CLOSED: {
      __proto__: null,
      configurable: false,
      enumerable: true,
      value: CLOSED,
      writable: false
    }
  };
  Object.defineProperties(EventSource, constantsPropertyDescriptors);
  Object.defineProperties(EventSource.prototype, constantsPropertyDescriptors);
  Object.defineProperties(EventSource.prototype, {
    close: kEnumerableProperty,
    onerror: kEnumerableProperty,
    onmessage: kEnumerableProperty,
    onopen: kEnumerableProperty,
    readyState: kEnumerableProperty,
    url: kEnumerableProperty,
    withCredentials: kEnumerableProperty
  });
  webidl.converters.EventSourceInitDict = webidl.dictionaryConverter([
    {
      key: "withCredentials",
      converter: webidl.converters.boolean,
      defaultValue: () => false
    },
    {
      key: "dispatcher",
      converter: webidl.converters.any
    }
  ]);
  module.exports = {
    EventSource,
    defaultReconnectionTime
  };
});

// server/lib/kiro/api-client.ts
import { randomUUID as randomUUID2 } from "crypto";

// server/lib/kiro/auth-method.ts
var KIRO_API_KEY_TOKEN_TYPE = "API_KEY";
function isApiKeyAuthMethod(method) {
  switch (method?.trim().toLowerCase()) {
    case "api_key":
    case "apikey":
    case "api-key":
      return true;
    default:
      return false;
  }
}
function canonicalizeAuthMethodValue(method) {
  const normalized = method?.trim().toLowerCase();
  if (!normalized)
    return null;
  if (isApiKeyAuthMethod(normalized))
    return "api_key";
  if (normalized === "idc" || normalized === "builder-id" || normalized === "iam")
    return "idc";
  if (normalized === "social")
    return "social";
  return null;
}
function canonicalKiroAuthMethod(creds) {
  const explicit = canonicalizeAuthMethodValue(creds.authMethod);
  if (explicit)
    return explicit;
  if (!creds.refreshToken && apiKeyOf(creds))
    return "api_key";
  if (creds.clientId && creds.clientSecret)
    return "idc";
  return "social";
}
function apiKeyOf(creds) {
  const key = creds.apiKey?.trim();
  if (key)
    return key;
  if (isApiKeyAuthMethod(creds.authMethod)) {
    const token = creds.accessToken?.trim();
    if (token)
      return token;
  }
  return;
}
function isApiKeyCredential(creds) {
  return canonicalKiroAuthMethod(creds) === "api_key" && !!apiKeyOf(creds);
}
function applyKiroTokenTypeHeader(headers, creds) {
  if (canonicalKiroAuthMethod(creds) === "api_key") {
    headers.TokenType = KIRO_API_KEY_TOKEN_TYPE;
  }
}

// server/lib/kiro/concurrency-control.ts
var INITIAL_BACKOFF_MS = 1000;
var MAX_BACKOFF_MS = 30000;
var SLOT_WAIT_TIMEOUT_MS = 300000;
var MAX_QUEUE_LENGTH = 20;
var RPM_WINDOW_MS = 60000;
var THROTTLE_WINDOW_MS = 60000;
var DEFAULT_PLAN_CONFIG = { initialMax: 3, ceiling: 8, floor: 1 };
var DEFAULT_CONCURRENCY_CONFIG = { default: { ...DEFAULT_PLAN_CONFIG } };
var DEFAULT_INTERVAL_CONFIG = { free: 0, enterprise: 0, paid: 0 };
var concurrencyConfig = { ...DEFAULT_CONCURRENCY_CONFIG };
var intervalConfig = { ...DEFAULT_INTERVAL_CONFIG };
var credentialPlanMap = new Map;
function classifyPlan(subscriptionTitle) {
  if (!subscriptionTitle)
    return "free";
  const lower = subscriptionTitle.toLowerCase();
  if (lower.includes("power"))
    return "power";
  if (lower.includes("pro+") || lower.includes("pro plus"))
    return "pro_plus";
  if (lower.includes("pro"))
    return "pro";
  if (lower.includes("free"))
    return "free";
  return;
}
function resolveConfig(subscriptionTitle) {
  const plan = classifyPlan(subscriptionTitle);
  if (plan && concurrencyConfig[plan])
    return concurrencyConfig[plan];
  return concurrencyConfig.default;
}
function resolveConfigForCredential(credentialId) {
  return resolveConfig(credentialPlanMap.get(credentialId));
}
var states = new Map;
var rpmWindows = new Map;
var throttleTotalCounts = new Map;
var throttleWindows = new Map;
function getOrCreate(credentialId, subscriptionTitle) {
  if (subscriptionTitle !== undefined) {
    credentialPlanMap.set(credentialId, subscriptionTitle ?? undefined);
  }
  let state = states.get(credentialId);
  if (!state) {
    const cfg = resolveConfig(subscriptionTitle ?? credentialPlanMap.get(credentialId));
    state = {
      maxConcurrency: cfg.initialMax,
      activeConcurrency: 0,
      lastThrottleTs: 0,
      consecutiveSuccess: 0,
      backoffMs: INITIAL_BACKOFF_MS,
      queue: [],
      lastRequestTs: 0,
      blockedUntil: 0,
      tier: "free",
      intervalTimer: null
    };
    states.set(credentialId, state);
  }
  return state;
}
function updateLimits(newConfig) {
  concurrencyConfig = newConfig ?? { ...DEFAULT_CONCURRENCY_CONFIG };
  if (!concurrencyConfig.default)
    concurrencyConfig.default = { ...DEFAULT_PLAN_CONFIG };
  for (const [id, state] of states) {
    const cfg = resolveConfigForCredential(id);
    state.maxConcurrency = Math.max(cfg.floor, Math.min(state.maxConcurrency, cfg.ceiling));
  }
}
function updateIntervalConfig(newConfig) {
  intervalConfig = newConfig ?? { ...DEFAULT_INTERVAL_CONFIG };
}
function getIntervalWaitMs(state) {
  const now = Date.now();
  const blockedWait = Math.max(0, state.blockedUntil - now);
  const minInterval = intervalConfig[state.tier] ?? 0;
  if (minInterval <= 0 || state.lastRequestTs <= 0)
    return blockedWait;
  const elapsed = now - state.lastRequestTs;
  const intervalWait = elapsed >= minInterval ? 0 : minInterval - elapsed;
  return Math.max(blockedWait, intervalWait);
}
function tryImmediateAcquire(state) {
  if (state.activeConcurrency < Math.floor(state.maxConcurrency) && getIntervalWaitMs(state) === 0) {
    state.activeConcurrency++;
    state.lastRequestTs = Date.now();
    return true;
  }
  return false;
}
function acquireSlot(credentialId, tier, subscriptionTitle) {
  const state = getOrCreate(credentialId, subscriptionTitle);
  if (tier !== undefined)
    state.tier = tier;
  if (tryImmediateAcquire(state))
    return Promise.resolve();
  if (state.queue.length >= MAX_QUEUE_LENGTH) {
    return Promise.reject(new Error("Credential concurrency queue full"));
  }
  return enqueueWaiter(state, false);
}
function acquireSlotPriority(credentialId, tier, subscriptionTitle) {
  const state = getOrCreate(credentialId, subscriptionTitle);
  if (tier !== undefined)
    state.tier = tier;
  if (tryImmediateAcquire(state))
    return Promise.resolve();
  if (state.queue.length >= MAX_QUEUE_LENGTH) {
    return Promise.reject(new Error("Credential concurrency queue full"));
  }
  return enqueueWaiter(state, true);
}
function enqueueWaiter(state, priority, onProgress) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      const idx = state.queue.findIndex((entry2) => entry2.resolve === resolve);
      if (idx !== -1)
        state.queue.splice(idx, 1);
      reject(new Error("Slot wait timeout"));
    }, SLOT_WAIT_TIMEOUT_MS);
    const entry = { resolve, reject, timer, onProgress };
    if (priority)
      state.queue.unshift(entry);
    else
      state.queue.push(entry);
    onProgress?.(state.queue.indexOf(entry) + 1, state.queue.length);
    scheduleIntervalDequeue(state);
  });
}
function releaseSlot(credentialId) {
  const state = states.get(credentialId);
  if (!state)
    return;
  state.activeConcurrency = Math.max(0, state.activeConcurrency - 1);
  tryDequeue(state);
  scheduleIntervalDequeue(state);
}
function onThrottle(credentialId) {
  const state = getOrCreate(credentialId);
  const now = Date.now();
  const cfg = resolveConfigForCredential(credentialId);
  state.maxConcurrency = Math.max(cfg.floor, state.maxConcurrency - 1);
  state.consecutiveSuccess = 0;
  if (now - state.lastThrottleTs < 5000) {
    state.backoffMs = Math.min(state.backoffMs * 2, MAX_BACKOFF_MS);
  } else {
    state.backoffMs = INITIAL_BACKOFF_MS;
  }
  state.lastThrottleTs = now;
  state.blockedUntil = Math.max(state.blockedUntil, now + state.backoffMs);
}
function onSuccess(credentialId) {
  const state = getOrCreate(credentialId);
  const cfg = resolveConfigForCredential(credentialId);
  state.consecutiveSuccess++;
  state.backoffMs = Math.max(500, state.backoffMs * 0.8);
  const threshold = Math.floor(state.maxConcurrency) * 3;
  if (threshold > 0 && state.consecutiveSuccess >= threshold) {
    state.maxConcurrency = Math.min(state.maxConcurrency + 1, cfg.ceiling);
    state.consecutiveSuccess = 0;
  }
}
function getBackoffMs(credentialId) {
  return states.get(credentialId)?.backoffMs ?? INITIAL_BACKOFF_MS;
}
function touchLastRequest(credentialId) {
  const state = states.get(credentialId);
  if (state)
    state.lastRequestTs = Date.now();
}
function getHeadroom(credentialId) {
  const state = states.get(credentialId);
  if (!state)
    return resolveConfigForCredential(credentialId).initialMax;
  return Math.max(0, Math.floor(state.maxConcurrency) - state.activeConcurrency);
}
function getState(credentialId) {
  const state = states.get(credentialId);
  if (!state) {
    const cfg = resolveConfigForCredential(credentialId);
    return {
      maxConcurrency: cfg.initialMax,
      activeConcurrency: 0,
      queueDepth: 0,
      lastThrottleTs: 0,
      consecutiveSuccess: 0,
      backoffMs: INITIAL_BACKOFF_MS,
      lastRequestTs: 0,
      blockedUntil: 0
    };
  }
  return {
    maxConcurrency: state.maxConcurrency,
    activeConcurrency: state.activeConcurrency,
    queueDepth: state.queue.length,
    lastThrottleTs: state.lastThrottleTs,
    consecutiveSuccess: state.consecutiveSuccess,
    backoffMs: state.backoffMs,
    lastRequestTs: state.lastRequestTs,
    blockedUntil: state.blockedUntil
  };
}
function getAllStates() {
  const result = {};
  for (const [id] of states)
    result[id] = getState(id);
  return result;
}
function tryDequeue(state) {
  let dequeued = false;
  while (state.queue.length > 0 && state.activeConcurrency < Math.floor(state.maxConcurrency)) {
    if (getIntervalWaitMs(state) > 0)
      break;
    const waiter = state.queue.shift();
    if (!waiter)
      break;
    clearTimeout(waiter.timer);
    state.activeConcurrency++;
    state.lastRequestTs = Date.now();
    waiter.resolve();
    dequeued = true;
  }
  if (dequeued)
    notifyQueuePositions(state);
}
function scheduleIntervalDequeue(state) {
  if (state.intervalTimer)
    return;
  if (state.queue.length === 0)
    return;
  if (state.activeConcurrency >= Math.floor(state.maxConcurrency))
    return;
  const waitMs = getIntervalWaitMs(state);
  if (waitMs <= 0)
    return;
  state.intervalTimer = setTimeout(() => {
    state.intervalTimer = null;
    tryDequeue(state);
    scheduleIntervalDequeue(state);
  }, waitMs);
}
function notifyQueuePositions(state) {
  const depth = state.queue.length;
  for (let i = 0;i < depth; i++) {
    state.queue[i].onProgress?.(i + 1, depth);
  }
}
function recordRequest(credentialId) {
  const now = Date.now();
  const timestamps = rpmWindows.get(credentialId) ?? [];
  timestamps.push(now);
  pruneWindow(timestamps, now - RPM_WINDOW_MS);
  rpmWindows.set(credentialId, timestamps);
}
function getWindowRpm(credentialId) {
  const timestamps = rpmWindows.get(credentialId);
  if (!timestamps)
    return 0;
  pruneWindow(timestamps, Date.now() - RPM_WINDOW_MS);
  return timestamps.length;
}
function getRpmCounts() {
  const result = {};
  const cutoff = Date.now() - RPM_WINDOW_MS;
  for (const [id, timestamps] of rpmWindows) {
    pruneWindow(timestamps, cutoff);
    if (timestamps.length > 0)
      result[id] = timestamps.length;
  }
  return result;
}
function recordThrottle(credentialId) {
  throttleTotalCounts.set(credentialId, (throttleTotalCounts.get(credentialId) ?? 0) + 1);
  const now = Date.now();
  const timestamps = throttleWindows.get(credentialId) ?? [];
  timestamps.push(now);
  pruneWindow(timestamps, now - THROTTLE_WINDOW_MS);
  throttleWindows.set(credentialId, timestamps);
}
function getThrottleCounts() {
  const result = {};
  const cutoff = Date.now() - THROTTLE_WINDOW_MS;
  const ids = new Set([...throttleTotalCounts.keys(), ...throttleWindows.keys()]);
  for (const id of ids) {
    const timestamps = throttleWindows.get(id) ?? [];
    pruneWindow(timestamps, cutoff);
    const total = throttleTotalCounts.get(id) ?? 0;
    if (total > 0 || timestamps.length > 0) {
      result[id] = { recent: timestamps.length, total };
    }
  }
  return result;
}
function pruneWindow(timestamps, cutoff) {
  while (timestamps.length > 0 && timestamps[0] < cutoff)
    timestamps.shift();
}

// server/lib/kiro/config.ts
import {
  chmodSync,
  existsSync,
  mkdirSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync
} from "fs";
import { dirname, join } from "path";
function writeSecureJson(filePath, value) {
  const dir = dirname(filePath);
  mkdirSync(dir, { recursive: true });
  const tempPath = `${filePath}.${process.pid}.${Date.now()}.tmp`;
  try {
    writeFileSync(tempPath, JSON.stringify(value, null, 2), { mode: 384 });
    renameSync(tempPath, filePath);
    try {
      chmodSync(filePath, 384);
    } catch {}
  } catch (error) {
    rmSync(tempPath, { force: true });
    throw error;
  }
}
var SYSTEM_VERSIONS = ["darwin#24.6.0", "win32#10.0.22631"];
function randomSystemVersion() {
  return SYSTEM_VERSIONS[Math.floor(Math.random() * SYSTEM_VERSIONS.length)];
}
var DEFAULT_CONCURRENCY_PLAN = { initialMax: 3, ceiling: 8, floor: 1 };
var DEFAULT_CONCURRENCY = { default: { ...DEFAULT_CONCURRENCY_PLAN } };
var DEFAULT_REQUEST_INTERVAL = { free: 0, enterprise: 0, paid: 0 };
var DEFAULT_TIER_PRIORITY = ["free", "enterprise", "paid"];
var CONFIG_DEFAULTS = {
  region: "us-east-1",
  kiroVersion: "1.26.1",
  systemVersion: randomSystemVersion(),
  nodeVersion: "22.21.1",
  loadBalancingMode: "priority",
  ignoreTierPriority: false,
  tierPriority: [...DEFAULT_TIER_PRIORITY],
  concurrency: { ...DEFAULT_CONCURRENCY },
  requestInterval: { ...DEFAULT_REQUEST_INTERVAL }
};
function canonicalizeAuthMethod(creds) {
  const lower = creds.authMethod?.toLowerCase();
  if (lower === "iam") {
    creds.authMethod = "idc";
    return;
  }
  if (isApiKeyAuthMethod(lower) || !creds.refreshToken && creds.apiKey?.trim()) {
    const key = creds.apiKey?.trim() || creds.accessToken?.trim();
    if (!key)
      return;
    creds.authMethod = "api_key";
    creds.apiKey = key;
    creds.accessToken = key;
  }
}
function parseTierPriority(value) {
  if (!Array.isArray(value))
    return [...DEFAULT_TIER_PRIORITY];
  const result = value.filter((v) => v === "free" || v === "enterprise" || v === "paid");
  return result.length > 0 ? result : [...DEFAULT_TIER_PRIORITY];
}
function parsePlanConfig(value) {
  if (!value || typeof value !== "object" || Array.isArray(value))
    return;
  const record = value;
  const initialMax = Number(record.initialMax);
  const ceiling = Number(record.ceiling);
  const floor = Number(record.floor);
  if (!Number.isFinite(initialMax) || !Number.isFinite(ceiling) || !Number.isFinite(floor)) {
    return;
  }
  return {
    initialMax: Math.max(1, Math.floor(initialMax)),
    ceiling: Math.max(1, Math.floor(ceiling)),
    floor: Math.max(1, Math.floor(floor))
  };
}
function parseConcurrency(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return { default: { ...DEFAULT_CONCURRENCY_PLAN } };
  }
  const record = value;
  const parsed = {
    default: parsePlanConfig(record.default) ?? { ...DEFAULT_CONCURRENCY_PLAN }
  };
  for (const key of ["free", "pro", "pro_plus", "power"]) {
    const cfg = parsePlanConfig(record[key]);
    if (cfg)
      parsed[key] = cfg;
  }
  return parsed;
}
function parseRequestInterval(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return { ...DEFAULT_REQUEST_INTERVAL };
  }
  const record = value;
  return {
    free: Math.max(0, Number(record.free) || 0),
    enterprise: Math.max(0, Number(record.enterprise) || 0),
    paid: Math.max(0, Number(record.paid) || 0)
  };
}
function loadConfig(path) {
  if (!existsSync(path))
    return { ...CONFIG_DEFAULTS };
  try {
    const raw = readFileSync(path, "utf-8").trim();
    if (!raw)
      return { ...CONFIG_DEFAULTS };
    const parsed = JSON.parse(raw);
    return {
      region: String(parsed.region ?? CONFIG_DEFAULTS.region),
      authRegion: typeof parsed.authRegion === "string" ? parsed.authRegion : undefined,
      apiRegion: typeof parsed.apiRegion === "string" ? parsed.apiRegion : undefined,
      kiroVersion: String(parsed.kiroVersion ?? CONFIG_DEFAULTS.kiroVersion),
      machineId: typeof parsed.machineId === "string" ? parsed.machineId : undefined,
      systemVersion: String(parsed.systemVersion ?? CONFIG_DEFAULTS.systemVersion),
      nodeVersion: String(parsed.nodeVersion ?? CONFIG_DEFAULTS.nodeVersion),
      loadBalancingMode: parsed.loadBalancingMode === "balanced" ? "balanced" : "priority",
      ignoreTierPriority: parsed.ignoreTierPriority === true,
      tierPriority: parseTierPriority(parsed.tierPriority),
      concurrency: parseConcurrency(parsed.concurrency),
      requestInterval: parseRequestInterval(parsed.requestInterval)
    };
  } catch {
    return { ...CONFIG_DEFAULTS };
  }
}
var DEFAULT_KIRO_REGION = "us-east-1";
function trimmedOrUndefined(value) {
  const trimmed = value?.trim();
  return trimmed || undefined;
}
function effectiveAuthRegion(creds, config) {
  return trimmedOrUndefined(creds.authRegion) ?? trimmedOrUndefined(creds.region) ?? trimmedOrUndefined(config.authRegion) ?? trimmedOrUndefined(config.region) ?? DEFAULT_KIRO_REGION;
}
function effectiveApiRegion(config, creds) {
  return trimmedOrUndefined(creds?.apiRegion) ?? trimmedOrUndefined(config.apiRegion) ?? trimmedOrUndefined(config.region) ?? DEFAULT_KIRO_REGION;
}
function loadAllCredentials(path) {
  if (!existsSync(path))
    return { credentials: [], isMultipleFormat: false };
  try {
    const raw = readFileSync(path, "utf-8").trim();
    if (!raw)
      return { credentials: [], isMultipleFormat: false };
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      for (const c of parsed)
        canonicalizeAuthMethod(c);
      const sorted = parsed.sort((a, b) => (a.priority ?? 0) - (b.priority ?? 0));
      return { credentials: sorted, isMultipleFormat: true };
    }
    canonicalizeAuthMethod(parsed);
    return { credentials: [parsed], isMultipleFormat: false };
  } catch {
    return { credentials: [], isMultipleFormat: false };
  }
}
function saveAllCredentials(path, creds) {
  writeSecureJson(path, creds);
}
function loadStats(credentialsPath) {
  const statsPath = join(dirname(credentialsPath), "kiro_stats.json");
  if (!existsSync(statsPath))
    return {};
  try {
    return JSON.parse(readFileSync(statsPath, "utf-8"));
  } catch {
    return {};
  }
}
function saveStats(credentialsPath, stats) {
  const statsPath = join(dirname(credentialsPath), "kiro_stats.json");
  writeSecureJson(statsPath, stats);
}
function loadUsageCache(credentialsPath) {
  const usagePath = join(dirname(credentialsPath), "kiro_usage.json");
  if (!existsSync(usagePath))
    return {};
  try {
    return JSON.parse(readFileSync(usagePath, "utf-8"));
  } catch {
    return {};
  }
}
function saveUsageCache(credentialsPath, cache) {
  const usagePath = join(dirname(credentialsPath), "kiro_usage.json");
  writeSecureJson(usagePath, cache);
}

// server/lib/kiro/multi-token-manager.ts
import { createHash as createHash2, randomUUID } from "crypto";

// node_modules/nanoid/index.js
import { webcrypto as crypto } from "crypto";

// node_modules/nanoid/url-alphabet/index.js
var urlAlphabet = "useandom-26T198340PX75pxJACKVERYMINDBUSHWOLF_GQZbfghjklqvwyzrict";

// node_modules/nanoid/index.js
var POOL_SIZE_MULTIPLIER = 128;
var pool;
var poolOffset;
function fillPool(bytes) {
  if (!pool || pool.length < bytes) {
    pool = Buffer.allocUnsafe(bytes * POOL_SIZE_MULTIPLIER);
    crypto.getRandomValues(pool);
    poolOffset = 0;
  } else if (poolOffset + bytes > pool.length) {
    crypto.getRandomValues(pool);
    poolOffset = 0;
  }
  poolOffset += bytes;
}
function nanoid(size = 21) {
  fillPool(size |= 0);
  let id = "";
  for (let i = poolOffset - size;i < poolOffset; i++) {
    id += urlAlphabet[pool[i] & 63];
  }
  return id;
}

// server/lib/id.ts
function generateShortId(size = 8) {
  return nanoid(size);
}

// server/lib/logger.ts
import { appendFileSync, existsSync as existsSync2, mkdirSync as mkdirSync2, renameSync as renameSync2, statSync, unlinkSync } from "fs";
import { resolve as resolve2 } from "path";

// server/lib/narrafork-home.ts
import { homedir } from "os";
import { resolve } from "path";
function getNarraforkHome() {
  return resolve(process.env.NARRAFORK_HOME?.trim() || resolve(homedir(), ".narrafork"));
}

// server/lib/logger.ts
var narraforkDir = getNarraforkHome();
mkdirSync2(narraforkDir, { recursive: true });
var logPath = resolve2(narraforkDir, "server.log");
var LEVEL_PRIORITY = {
  debug: 10,
  info: 20,
  warn: 30,
  error: 40
};
var consoleMethods = {
  debug: (msg) => console.debug(msg),
  info: (msg) => console.info(msg),
  warn: (msg) => console.warn(msg),
  error: (msg) => console.error(msg)
};
var DEFAULT_LOG_LEVEL = "info";
var DEFAULT_MAX_LOG_BYTES = 50 * 1024 * 1024;
var DEFAULT_MAX_LOG_FILES = 5;
var currentLogBytes = readCurrentLogSize();
function parseLogLevel(value) {
  if (value === "debug" || value === "info" || value === "warn" || value === "error") {
    return value;
  }
  return DEFAULT_LOG_LEVEL;
}
function parsePositiveInt(value, fallback) {
  if (!value)
    return fallback;
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}
function parseNonNegativeInt(value, fallback) {
  if (!value)
    return fallback;
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback;
}
function readCurrentLogSize() {
  try {
    return statSync(logPath).size;
  } catch {
    return 0;
  }
}
var configuredLevel = parseLogLevel(process.env.NARRAFORK_LOG_LEVEL);
var maxLogBytes = parsePositiveInt(process.env.NARRAFORK_LOG_MAX_BYTES, DEFAULT_MAX_LOG_BYTES);
var maxLogFiles = parseNonNegativeInt(process.env.NARRAFORK_LOG_MAX_FILES, DEFAULT_MAX_LOG_FILES);
function shouldLog(level) {
  return LEVEL_PRIORITY[level] >= LEVEL_PRIORITY[configuredLevel];
}
function pad(value, length = 2) {
  return String(value).padStart(length, "0");
}
function formatLocalTimestamp(date = new Date) {
  const offsetMinutes = -date.getTimezoneOffset();
  const sign = offsetMinutes >= 0 ? "+" : "-";
  const absOffsetMinutes = Math.abs(offsetMinutes);
  const offsetHours = Math.floor(absOffsetMinutes / 60);
  const offsetRemainderMinutes = absOffsetMinutes % 60;
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}${sign}${pad(offsetHours)}:${pad(offsetRemainderMinutes)}`;
}
function rotateLogsIfNeeded(nextBytes) {
  if (maxLogBytes <= 0 || currentLogBytes + nextBytes <= maxLogBytes)
    return;
  try {
    if (maxLogFiles <= 0) {
      if (existsSync2(logPath))
        unlinkSync(logPath);
      currentLogBytes = 0;
      return;
    }
    const oldest = `${logPath}.${maxLogFiles}`;
    if (existsSync2(oldest))
      unlinkSync(oldest);
    for (let i = maxLogFiles - 1;i >= 1; i--) {
      const from = `${logPath}.${i}`;
      const to = `${logPath}.${i + 1}`;
      if (existsSync2(from))
        renameSync2(from, to);
    }
    if (existsSync2(logPath))
      renameSync2(logPath, `${logPath}.1`);
    currentLogBytes = 0;
  } catch {
    currentLogBytes = readCurrentLogSize();
  }
}
function log(level, message, data) {
  if (!shouldLog(level))
    return;
  const entry = JSON.stringify({ ts: formatLocalTimestamp(), level, msg: message, ...data });
  consoleMethods[level](entry);
  try {
    const line = `${entry}
`;
    const bytes = Buffer.byteLength(line, "utf-8");
    rotateLogsIfNeeded(bytes);
    appendFileSync(logPath, line);
    currentLogBytes += bytes;
  } catch {}
}
var logger = {
  debug: (msg, data) => log("debug", msg, data),
  info: (msg, data) => log("info", msg, data),
  warn: (msg, data) => log("warn", msg, data),
  error: (msg, data) => log("error", msg, data)
};

// server/lib/kiro/machine-id.ts
import { createHash } from "crypto";
function normalizeMachineId(machineId) {
  const trimmed = machineId.trim();
  if (trimmed.length === 64 && /^[0-9a-fA-F]+$/.test(trimmed)) {
    return trimmed;
  }
  const withoutDashes = trimmed.split("-").join("");
  if (withoutDashes.length === 32 && /^[0-9a-fA-F]+$/.test(withoutDashes)) {
    return `${withoutDashes}${withoutDashes}`;
  }
  return null;
}
function sha256Hex(input) {
  return createHash("sha256").update(input).digest("hex");
}
function generateMachineId(creds, config) {
  if (creds.machineId) {
    const normalized = normalizeMachineId(creds.machineId);
    if (normalized)
      return normalized;
  }
  if (config.machineId) {
    const normalized = normalizeMachineId(config.machineId);
    if (normalized)
      return normalized;
  }
  const secret = creds.refreshToken || creds.apiKey;
  if (secret) {
    return sha256Hex(`KotlinNativeAPI/${secret}`);
  }
  return null;
}

// server/lib/net/diagnostic-redaction.ts
var REDACTED = "********";
var MAX_DIAGNOSTIC_TEXT_CHARS = 4000;
var SAFE_QUERY_PARAMS = new Set(["api-version", "version"]);
var SAFE_HEADER_NAMES = new Set([
  "accept",
  "accept-encoding",
  "content-encoding",
  "content-length",
  "content-type",
  "retry-after",
  "request-id",
  "server",
  "user-agent",
  "via",
  "x-amzn-requestid",
  "x-request-id",
  "cf-ray"
]);
function isSafeHeaderName(name) {
  const normalized = name.trim().toLowerCase();
  return SAFE_HEADER_NAMES.has(normalized) || /^(?:x-)?rate-?limit(?:-|$)/.test(normalized) || /^ratelimit(?:-|$)/.test(normalized);
}
function truncate(value) {
  if (value.length <= MAX_DIAGNOSTIC_TEXT_CHARS)
    return value;
  return `${value.slice(0, MAX_DIAGNOSTIC_TEXT_CHARS)}\u2026`;
}
function redactSecretPatterns(value) {
  return value.replace(/\b((?:authorization|proxy-authorization)\s*[:=]\s*)((?:Bearer|Basic)\s+[^\s,;]+|[^\s,;]+)/gi, `$1${REDACTED}`).replace(/\b(Bearer|Basic)\s+[^\s,;]+/gi, `$1 ${REDACTED}`).replace(/((?:x-api-key|api-key|api[_-]?key|access[_-]?token|refresh[_-]?token|token|secret|password|signature)\s*[:=]\s*)([^\s,;]+)/gi, `$1${REDACTED}`).replace(/\beyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\b/g, REDACTED);
}
function redactDiagnosticUrl(value) {
  const raw = value instanceof URL ? value.toString() : value;
  try {
    const url = new URL(raw);
    url.username = "";
    url.password = "";
    url.hash = "";
    for (const key of [...url.searchParams.keys()]) {
      if (!SAFE_QUERY_PARAMS.has(key.toLowerCase()))
        url.searchParams.set(key, REDACTED);
    }
    return url.toString();
  } catch {
    return truncate(redactSecretPatterns(raw));
  }
}
function redactDiagnosticText(value) {
  const withoutUnsafeVerboseHint = value.replace(/\.\s*For more information, pass `verbose: true` in the second argument to fetch\(\)/gi, ".");
  const masked = redactSecretPatterns(withoutUnsafeVerboseHint).replace(/\b(?:https?|socks(?:4a?|5h?)):\/\/[^\s<>"']+/gi, (url) => redactDiagnosticUrl(url));
  return truncate(masked);
}
function redactDiagnosticHeader(name, value) {
  if (!isSafeHeaderName(name))
    return REDACTED;
  return redactSecretPatterns(value);
}
function redactDiagnosticHeaders(headers) {
  if (!headers)
    return [];
  try {
    return [...new Headers(headers).entries()].map(([name, value]) => `${name}: ${redactDiagnosticHeader(name, value)}`);
  } catch {
    return [`headers: ${REDACTED}`];
  }
}
function selectSafeDiagnosticHeaders(headers) {
  const selected = {};
  for (const [name, value] of headers.entries()) {
    if (!isSafeHeaderName(name))
      continue;
    selected[name] = redactDiagnosticHeader(name, value);
  }
  return Object.keys(selected).length > 0 ? selected : undefined;
}

// node_modules/undici/index.js
var Client = require_client();
var Dispatcher = require_dispatcher();
var Pool = require_pool();
var BalancedPool = require_balanced_pool();
var Agent = require_agent();
var ProxyAgent = require_proxy_agent();
var EnvHttpProxyAgent = require_env_http_proxy_agent();
var RetryAgent = require_retry_agent();
var errors = require_errors();
var util = require_util();
var { InvalidArgumentError } = errors;
var api = require_api();
var buildConnector = require_connect();
var MockClient = require_mock_client();
var MockAgent = require_mock_agent();
var MockPool = require_mock_pool();
var mockErrors = require_mock_errors();
var RetryHandler = require_retry_handler();
var { getGlobalDispatcher, setGlobalDispatcher } = require_global2();
var DecoratorHandler = require_decorator_handler();
var RedirectHandler = require_redirect_handler();
var createRedirectInterceptor = require_redirect_interceptor();
Object.assign(Dispatcher.prototype, api);
var $interceptors = {
  redirect: require_redirect(),
  retry: require_retry(),
  dump: require_dump(),
  dns: require_dns()
};
var $util = {
  parseHeaders: util.parseHeaders,
  headerNameToString: util.headerNameToString
};
function makeDispatcher(fn) {
  return (url, opts, handler) => {
    if (typeof opts === "function") {
      handler = opts;
      opts = null;
    }
    if (!url || typeof url !== "string" && typeof url !== "object" && !(url instanceof URL)) {
      throw new InvalidArgumentError("invalid url");
    }
    if (opts != null && typeof opts !== "object") {
      throw new InvalidArgumentError("invalid opts");
    }
    if (opts && opts.path != null) {
      if (typeof opts.path !== "string") {
        throw new InvalidArgumentError("invalid opts.path");
      }
      let path = opts.path;
      if (!opts.path.startsWith("/")) {
        path = `/${path}`;
      }
      url = new URL(util.parseOrigin(url).origin + path);
    } else {
      if (!opts) {
        opts = typeof url === "object" ? url : {};
      }
      url = util.parseURL(url);
    }
    const { agent, dispatcher = getGlobalDispatcher() } = opts;
    if (agent) {
      throw new InvalidArgumentError("unsupported opts.agent. Did you mean opts.client?");
    }
    return fn.call(dispatcher, {
      ...opts,
      origin: url.origin,
      path: url.search ? `${url.pathname}${url.search}` : url.pathname,
      method: opts.method || (opts.body ? "PUT" : "GET")
    }, handler);
  };
}
var fetchImpl = require_fetch().fetch;
var $Headers = require_headers().Headers;
var $Response = require_response().Response;
var $Request = require_request2().Request;
var $FormData = require_formdata().FormData;
var $File = globalThis.File ?? __require("buffer").File;
var $FileReader = require_filereader().FileReader;
var { setGlobalOrigin, getGlobalOrigin } = require_global();
var { CacheStorage } = require_cachestorage();
var { kConstruct } = require_symbols4();
var $caches = new CacheStorage(kConstruct);
var { deleteCookie, getCookies, getSetCookies, setCookie } = require_cookies();
var { parseMIMEType, serializeAMimeType } = require_data_url();
var { CloseEvent, ErrorEvent, MessageEvent } = require_events();
var $WebSocket = require_websocket().WebSocket;
var $request = makeDispatcher(api.request);
var $stream = makeDispatcher(api.stream);
var $pipeline = makeDispatcher(api.pipeline);
var $connect = makeDispatcher(api.connect);
var $upgrade = makeDispatcher(api.upgrade);
var { EventSource } = require_eventsource();

// server/lib/net/tls-transport-error.ts
var TRANSIENT_TLS_HANDSHAKE_CODE = "UNKNOWN_CERTIFICATE_VERIFICATION_ERROR";
var MAX_CAUSE_DEPTH = 5;
function isTransientTlsHandshakeCode(code) {
  return typeof code === "string" && code.trim().toUpperCase() === TRANSIENT_TLS_HANDSHAKE_CODE;
}
function isTransientTlsHandshakeMessage(message) {
  return typeof message === "string" && message.toUpperCase().includes(TRANSIENT_TLS_HANDSHAKE_CODE);
}
function isTransientTlsHandshakeError(error) {
  let current = error;
  for (let depth = 0;current && typeof current === "object" && depth < MAX_CAUSE_DEPTH; depth++) {
    const value = current;
    if (isTransientTlsHandshakeCode(value.code))
      return true;
    if (isTransientTlsHandshakeMessage(value.message))
      return true;
    if (!value.cause || value.cause === current)
      return false;
    current = value.cause;
  }
  return typeof error === "string" && isTransientTlsHandshakeMessage(error);
}

// server/lib/net/outbound-fetch.ts
var MAX_VERBOSE_OUTPUT_CHARS = 64 * 1024;
var outboundFetchOverride = null;

class OutboundProxyConfigurationError extends Error {
  code;
  protocol;
  constructor(options) {
    const supported = options.supportedProtocols ?? ["http", "https"];
    const message = options.code === "INVALID_OUTBOUND_PROXY_URL" ? "The configured outbound proxy URL is invalid." : `Unsupported outbound proxy protocol "${options.protocol ?? "unknown"}". ` + `Supported protocols are ${supported.join(", ")}.`;
    super(message);
    this.name = "OutboundProxyConfigurationError";
    this.code = options.code;
    this.protocol = options.protocol;
  }
}
var SUPPORTED_PROXY_PROTOCOLS = new Set(["http:", "https:"]);
function normalizeProxyUrl(proxyUrl, supportedProtocols) {
  const trimmed = proxyUrl.trim();
  const normalized = /^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed) ? trimmed : `http://${trimmed}`;
  let parsed;
  try {
    parsed = new URL(normalized);
  } catch {
    throw new OutboundProxyConfigurationError({ code: "INVALID_OUTBOUND_PROXY_URL" });
  }
  if (!supportedProtocols.has(parsed.protocol.toLowerCase())) {
    throw new OutboundProxyConfigurationError({
      code: "UNSUPPORTED_OUTBOUND_PROXY_PROTOCOL",
      protocol: parsed.protocol.replace(/:$/, "").toLowerCase(),
      supportedProtocols: [...supportedProtocols].map((protocol) => protocol.replace(/:$/, ""))
    });
  }
  return parsed;
}
function writeVerboseBlock(lines) {
  const output = lines.join(`
`);
  console.error(output.length <= MAX_VERBOSE_OUTPUT_CHARS ? output : `${output.slice(0, MAX_VERBOSE_OUTPUT_CHARS)}
... [verbose output truncated]`);
}
var RETRYABLE_TRANSPORT_CODES = new Set([
  "ECONNRESET",
  "EPIPE",
  "ETIMEDOUT",
  "EAI_AGAIN",
  "ENETDOWN",
  "ENETUNREACH",
  "ECONNREFUSED",
  "UND_ERR_SOCKET",
  "UND_ERR_CONNECT_TIMEOUT",
  "UND_ERR_HEADERS_TIMEOUT",
  "CONNECTIONREFUSED",
  "CONNECTIONRESET",
  "CONNECTIONABORTED"
]);
var RETRYABLE_TRANSPORT_PATTERNS = [
  "socket connection was closed unexpectedly",
  "connection was closed unexpectedly",
  "socket hang up",
  "connection reset",
  "econnreset",
  "etimedout",
  "eai_again",
  "fetch failed",
  "failed to fetch",
  "network error",
  "unable to connect",
  "the operation timed out"
];
function isAbortError(error, signal) {
  if (signal?.aborted)
    return true;
  return error instanceof Error && error.name === "AbortError";
}
function isRetryableTransportError(error) {
  let current = error;
  let depth = 0;
  const messages = [];
  while (current && typeof current === "object" && depth < 5) {
    const value = current;
    if (typeof value.code === "string" && RETRYABLE_TRANSPORT_CODES.has(value.code.toUpperCase())) {
      return true;
    }
    if (typeof value.message === "string")
      messages.push(value.message.toLowerCase());
    if (!value.cause || value.cause === current)
      break;
    current = value.cause;
    depth++;
  }
  return messages.some((message) => RETRYABLE_TRANSPORT_PATTERNS.some((pattern) => message.includes(pattern)));
}
var IDEMPOTENT_HTTP_METHODS = new Set(["GET", "HEAD", "PUT", "DELETE", "OPTIONS", "TRACE"]);
function retryPolicyAllowsMethod(policy, method) {
  if (policy === "never")
    return false;
  return policy === "always" || IDEMPOTENT_HTTP_METHODS.has(method);
}
function prepareRetryInput(input, body, allowRequestBodyClone) {
  if (body instanceof ReadableStream)
    return;
  if (!(input instanceof Request))
    return input;
  if (!allowRequestBodyClone && input.body !== null)
    return;
  try {
    return input.clone();
  } catch {
    return;
  }
}
function shouldReplayTransportFailure(error, retryPolicy, methodReplayAllowed) {
  if (retryPolicy === "never")
    return false;
  if (isTransientTlsHandshakeError(error))
    return true;
  return methodReplayAllowed && isRetryableTransportError(error);
}
async function outboundFetch(input, init, options = {}) {
  if (outboundFetchOverride)
    return outboundFetchOverride(input, init);
  const rawUrl = input instanceof Request ? input.url : String(input);
  const method = (init?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase();
  const requestHeaders = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined));
  const proxy = options.proxyUrl ? normalizeProxyUrl(options.proxyUrl, SUPPORTED_PROXY_PROTOCOLS).toString() : "";
  const fetchInit = {
    ...init,
    headers: requestHeaders,
    proxy,
    ...options.tlsRejectUnauthorized === false ? { tls: { rejectUnauthorized: false } } : {}
  };
  const retryPolicy = options.retryPolicy ?? "never";
  const methodReplayAllowed = retryPolicyAllowsMethod(retryPolicy, method);
  const retryInput = retryPolicy === "never" ? undefined : prepareRetryInput(input, init?.body, methodReplayAllowed);
  const attempts = retryInput === undefined ? [input] : [input, retryInput];
  const signal = init?.signal ?? (input instanceof Request ? input.signal : undefined);
  for (const [attempt, attemptInput] of attempts.entries()) {
    if (options.verbose) {
      writeVerboseBlock([
        `> ${method} ${redactDiagnosticUrl(rawUrl)}${attempt > 0 ? " [retry]" : ""}`,
        ...redactDiagnosticHeaders(requestHeaders).map((header) => `> ${header}`)
      ]);
    }
    try {
      const response = await fetch(attemptInput, fetchInit);
      if (options.verbose) {
        writeVerboseBlock([
          `< HTTP ${response.status} ${redactDiagnosticText(response.statusText)}`,
          ...redactDiagnosticHeaders(response.headers).map((header) => `< ${header}`)
        ]);
      }
      return response;
    } catch (error) {
      const shouldRetry = attempt + 1 < attempts.length && !isAbortError(error, signal) && shouldReplayTransportFailure(error, retryPolicy, methodReplayAllowed);
      if (options.verbose) {
        const message = redactDiagnosticText(error instanceof Error ? error.message : String(error));
        const code = error && typeof error === "object" && "code" in error ? String(error.code ?? "") : "";
        writeVerboseBlock([
          `< NETWORK ERROR${code ? ` ${code}` : ""}: ${message}${shouldRetry ? " [retrying]" : ""}`
        ]);
      }
      if (!shouldRetry)
        throw error;
    }
  }
  throw new Error("Outbound fetch exhausted without returning a response");
}

// server/lib/agent/request-url-tracker.ts
import { AsyncLocalStorage } from "async_hooks";
var storage = new AsyncLocalStorage;
var MAX_CAPTURED_URLS = 100;
function recordRequestAttempt(request) {
  const store = storage.getStore();
  if (!store || store.requests.length >= MAX_CAPTURED_URLS)
    return;
  const entry = { sequence: store.requests.length + 1, ...request };
  store.requests.push(entry);
  let completed = false;
  return (update) => {
    if (completed)
      return;
    completed = true;
    Object.assign(entry, update);
  };
}
function isVerboseRequestCaptureEnabled() {
  return storage.getStore()?.verbose ?? false;
}

// server/lib/agent/diagnostic-fetch.ts
var MAX_ERROR_CAUSE_DEPTH = 4;
var MAX_VERBOSE_OUTPUT_CHARS2 = 64 * 1024;
var NETWORK_ERROR_CATEGORIES = new Set([
  "http",
  "dns",
  "connection_refused",
  "connection_reset",
  "timeout",
  "tls",
  "proxy",
  "aborted",
  "network"
]);
var sanitizeDiagnosticUrl = redactDiagnosticUrl;
var sanitizeDiagnosticText = redactDiagnosticText;
function readScalar(value, key) {
  const candidate = value[key];
  if (typeof candidate === "string" || typeof candidate === "number")
    return candidate;
  return;
}
function normalizeTransportCode(code, message) {
  const normalized = String(code).toUpperCase();
  if (normalized === "UND_ERR_SOCKET" && /other side closed|socket hang up|connection reset/i.test(message)) {
    return "ECONNRESET";
  }
  if (normalized === "UNSUPPORTEDPROXYPROTOCOL") {
    return "UNSUPPORTED_OUTBOUND_PROXY_PROTOCOL";
  }
  return String(code);
}
function serializeDiagnosticError(error, depth = 0, seen = new WeakSet) {
  if (depth >= MAX_ERROR_CAUSE_DEPTH) {
    return { message: "[Further error causes omitted]" };
  }
  if (!error || typeof error !== "object") {
    return { message: sanitizeDiagnosticText(String(error)) };
  }
  if (seen.has(error)) {
    return { message: "[Circular error cause]" };
  }
  seen.add(error);
  const value = error;
  const rawMessage = typeof value.message === "string" && value.message ? value.message : error instanceof Error ? error.message : String(error);
  const details = {
    name: typeof value.name === "string" && value.name ? value.name : error instanceof Error ? error.name : undefined,
    message: sanitizeDiagnosticText(rawMessage)
  };
  const code = readScalar(value, "code");
  const errno = readScalar(value, "errno");
  const syscall = readScalar(value, "syscall");
  const address = readScalar(value, "address");
  const port = readScalar(value, "port");
  const hostname = readScalar(value, "hostname");
  const status = readScalar(value, "status") ?? readScalar(value, "statusCode");
  const reason = readScalar(value, "reason");
  const category = readScalar(value, "category");
  const path = readScalar(value, "path");
  if (typeof category === "string" && NETWORK_ERROR_CATEGORIES.has(category)) {
    details.category = category;
  }
  if (code !== undefined)
    details.code = normalizeTransportCode(code, rawMessage);
  if (errno !== undefined)
    details.errno = errno;
  if (syscall !== undefined)
    details.syscall = String(syscall);
  if (address !== undefined)
    details.address = String(address);
  if (port !== undefined)
    details.port = port;
  if (hostname !== undefined)
    details.hostname = String(hostname);
  if (typeof status === "number")
    details.status = status;
  if (reason !== undefined)
    details.reason = sanitizeDiagnosticText(String(reason));
  if (path !== undefined)
    details.path = sanitizeDiagnosticUrl(String(path));
  const nested = value.cause ?? (value.error instanceof Error ? value.error : undefined);
  if (nested !== undefined && nested !== error) {
    details.cause = serializeDiagnosticError(nested, depth + 1, seen);
    if (details.code === undefined && details.cause.code !== undefined) {
      details.code = details.cause.code;
    }
    if (details.category === undefined && details.cause.category !== undefined) {
      details.category = details.cause.category;
    }
  }
  return details;
}
function upperErrorCode(error) {
  return error.code?.trim().toUpperCase() ?? "";
}
function classifyNetworkError(error) {
  const causeCategory = error.cause ? classifyNetworkError(error.cause) : undefined;
  const code = upperErrorCode(error);
  const message = `${error.message} ${error.reason ?? ""}`.toLowerCase();
  if (error.name === "AbortError" || code === "ABORT_ERR" || message.includes("aborted")) {
    return "aborted";
  }
  if (["EAI_AGAIN", "ENOTFOUND", "DNSERROR", "DNSNOTFOUND"].includes(code) || message.includes("dns") || message.includes("name resolution") || message.includes("could not resolve host")) {
    return "dns";
  }
  if (["ECONNREFUSED", "CONNECTIONREFUSED", "UND_ERR_CONNECT"].includes(code) || message.includes("connection refused") || message.includes("unable to connect")) {
    return "connection_refused";
  }
  if (["ECONNRESET", "EPIPE", "CONNECTIONRESET", "CONNECTIONABORTED", "UND_ERR_SOCKET"].includes(code) || message.includes("socket connection was closed") || message.includes("connection was closed unexpectedly") || message.includes("socket hang up") || message.includes("connection reset") || message.includes("other side closed")) {
    return "connection_reset";
  }
  if (["ETIMEDOUT", "UND_ERR_CONNECT_TIMEOUT", "UND_ERR_HEADERS_TIMEOUT"].includes(code) || message.includes("timed out") || message.includes("timeout")) {
    return "timeout";
  }
  if (code.startsWith("CERT_") || code.includes("TLS") || code.includes("SSL") || message.includes("certificate") || message.includes("tls") || message.includes("ssl")) {
    return "tls";
  }
  if (message.includes("proxy"))
    return "proxy";
  return causeCategory && causeCategory !== "network" ? causeCategory : "network";
}
function categoryExplanation(category) {
  switch (category) {
    case "dns":
      return "DNS lookup failed before a connection could be established.";
    case "connection_refused":
      return "The target host or configured proxy refused the TCP connection.";
    case "connection_reset":
      return "The upstream server or an intermediary closed the connection before returning an HTTP response.";
    case "timeout":
      return "The connection or response exceeded the network timeout.";
    case "tls":
      return "The TLS handshake or certificate verification failed.";
    case "proxy":
      return "The configured proxy could not complete the request.";
    case "aborted":
      return "The request was cancelled.";
    default:
      return "The request failed before an HTTP response was received.";
  }
}
function requestBodyBytes(body) {
  if (body == null)
    return;
  if (typeof body === "string")
    return Buffer.byteLength(body);
  if (body instanceof URLSearchParams)
    return Buffer.byteLength(body.toString());
  if (body instanceof Blob)
    return body.size;
  if (body instanceof ArrayBuffer)
    return body.byteLength;
  if (ArrayBuffer.isView(body))
    return body.byteLength;
  return;
}
function isAbortError2(error, signal) {
  if (signal?.aborted)
    return true;
  if (!error || typeof error !== "object")
    return false;
  const value = error;
  return value.name === "AbortError" || String(value.code ?? "").toUpperCase() === "ABORT_ERR";
}
function writeSafeVerboseBlock(lines) {
  const output = lines.join(`
`);
  console.error(output.length <= MAX_VERBOSE_OUTPUT_CHARS2 ? output : `${output.slice(0, MAX_VERBOSE_OUTPUT_CHARS2)}
... [verbose output truncated]`);
}

class NetworkRequestError extends Error {
  category;
  code;
  errno;
  path;
  hostname;
  address;
  port;
  method;
  url;
  proxyUrl;
  durationMs;
  diagnostic;
  constructor(options) {
    const transportDiagnostic = options.diagnostic.code ? options.diagnostic : options.diagnostic.cause ?? options.diagnostic;
    const codeSuffix = transportDiagnostic.code ? `/${transportDiagnostic.code}` : "";
    const route = options.proxyUrl ? `proxy ${options.proxyUrl}` : "direct connection";
    super(`Network request failed [${options.category}${codeSuffix}] after ${options.durationMs} ms: ` + `${options.method} ${options.url} via ${route}. ${categoryExplanation(options.category)}`, { cause: options.cause });
    this.name = "NetworkRequestError";
    this.category = options.category;
    this.code = transportDiagnostic.code;
    this.errno = transportDiagnostic.errno;
    this.path = transportDiagnostic.path ?? options.diagnostic.path;
    this.hostname = transportDiagnostic.hostname ?? options.diagnostic.hostname;
    this.address = transportDiagnostic.address ?? options.diagnostic.address;
    this.port = transportDiagnostic.port ?? options.diagnostic.port;
    this.method = options.method;
    this.url = options.url;
    this.proxyUrl = options.proxyUrl;
    this.durationMs = options.durationMs;
    this.diagnostic = options.diagnostic;
  }
}
async function fetchWithNetworkDiagnostics(input, init, options = {}) {
  const rawUrl = input instanceof Request ? input.url : String(input);
  const url = sanitizeDiagnosticUrl(rawUrl);
  const method = (init?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase();
  const proxyUrl = options.proxy ? sanitizeDiagnosticUrl(options.proxy) : undefined;
  const verbose = isVerboseRequestCaptureEnabled();
  const startedAt = performance.now();
  const finishCapture = recordRequestAttempt({
    url,
    method,
    route: proxyUrl ? "proxy" : "direct",
    proxyUrl,
    requestBodyBytes: requestBodyBytes(init?.body),
    verbose
  });
  if (verbose) {
    const requestHeaders = init?.headers ?? (input instanceof Request ? input.headers : undefined);
    writeSafeVerboseBlock([
      `> ${method} ${url}`,
      ...redactDiagnosticHeaders(requestHeaders).map((header) => `> ${header}`)
    ]);
  }
  try {
    const response = await outboundFetch(input, init, {
      proxyUrl: options.proxy,
      tlsRejectUnauthorized: options.tls?.rejectUnauthorized,
      retryPolicy: options.retryPolicy ?? "idempotent-only"
    });
    const durationMs = Math.max(0, Math.round(performance.now() - startedAt));
    const responseHeaders = selectSafeDiagnosticHeaders(response.headers);
    finishCapture?.({
      outcome: response.ok ? "success" : "http_error",
      category: response.ok ? undefined : "http",
      durationMs,
      status: response.status,
      statusText: sanitizeDiagnosticText(response.statusText),
      responseHeaders
    });
    if (verbose) {
      writeSafeVerboseBlock([
        `< HTTP ${response.status} ${sanitizeDiagnosticText(response.statusText)}`,
        ...Object.entries(responseHeaders ?? {}).map(([name, value]) => `< ${name}: ${value}`)
      ]);
    }
    return response;
  } catch (error) {
    const durationMs = Math.max(0, Math.round(performance.now() - startedAt));
    const diagnostic = serializeDiagnosticError(error);
    const category = classifyNetworkError(diagnostic);
    if (verbose) {
      writeSafeVerboseBlock([
        `< NETWORK ERROR${diagnostic.code ? ` ${diagnostic.code}` : ""}: ${diagnostic.message}`
      ]);
    }
    finishCapture?.({
      outcome: category === "aborted" ? "aborted" : "network_error",
      category,
      durationMs,
      error: diagnostic
    });
    if (isAbortError2(error, init?.signal))
      throw error;
    throw new NetworkRequestError({
      cause: error,
      category,
      diagnostic,
      method,
      url,
      proxyUrl,
      durationMs
    });
  }
}

// server/lib/kiro/proxy.ts
var proxyResolver = null;
function registerProxyResolver(resolver) {
  proxyResolver = resolver;
}
function getProxy() {
  return proxyResolver?.();
}
function pfetch(input, init) {
  return fetchWithNetworkDiagnostics(input, init, { proxy: getProxy() });
}

// server/lib/kiro/types.ts
class KiroError extends Error {
  code;
  constructor(code, message) {
    super(message);
    this.code = code;
    this.name = "KiroError";
  }
}

class KiroAuthError extends KiroError {
  constructor(message) {
    super("AUTH_ERROR", message);
    this.name = "KiroAuthError";
  }
}

class KiroApiError extends KiroError {
  statusCode;
  constructor(code, message, statusCode) {
    super(code, message);
    this.statusCode = statusCode;
    this.name = "KiroApiError";
  }
}

class KiroQuotaError extends KiroError {
  constructor(message) {
    super("QUOTA_EXHAUSTED", message);
    this.name = "KiroQuotaError";
  }
}

class KiroParseError extends KiroError {
  kind;
  constructor(kind, message) {
    super("PARSE_ERROR", message);
    this.kind = kind;
    this.name = "KiroParseError";
  }
}

// server/lib/kiro/token-manager.ts
var IDC_AMZ_USER_AGENT = "aws-sdk-js/3.738.0 ua/2.1 os/other lang/js md/browser#unknown_unknown api/sso-oidc#3.738.0 m/E KiroIDE";
var API_KEY_EXPIRY_MS = 10 * 365 * 24 * 60 * 60 * 1000;
function apiKeyFarFutureExpiry() {
  return new Date(Date.now() + API_KEY_EXPIRY_MS).toISOString();
}
async function refreshCredentials(creds, config) {
  const authMethod = canonicalKiroAuthMethod(creds);
  if (authMethod === "api_key")
    return refreshApiKey(creds);
  const rt = creds.refreshToken;
  if (!rt)
    throw new KiroAuthError("Missing refreshToken");
  if (rt.length < 100 || rt.includes("...")) {
    throw new KiroAuthError(`refreshToken appears truncated (${rt.length} chars)`);
  }
  return authMethod === "idc" ? refreshIdc(creds, config) : refreshSocial(creds, config);
}
function refreshApiKey(creds) {
  const key = apiKeyOf(creds);
  if (!key)
    throw new KiroAuthError("api_key credential missing stored key");
  return {
    ...creds,
    authMethod: "api_key",
    apiKey: key,
    accessToken: key,
    expiresAt: apiKeyFarFutureExpiry()
  };
}
async function refreshSocial(creds, config) {
  const rt = creds.refreshToken ?? "";
  const region = effectiveAuthRegion(creds, config);
  const domain = `prod.${region}.auth.desktop.kiro.dev`;
  const url = `https://${domain}/refreshToken`;
  const machineId = generateMachineId(creds, config);
  if (!machineId) {
    throw new KiroAuthError("Cannot generate machineId for Social refresh");
  }
  const res = await pfetch(url, {
    method: "POST",
    headers: {
      Accept: "application/json, text/plain, */*",
      "Content-Type": "application/json",
      "User-Agent": `KiroIDE-${config.kiroVersion}-${machineId}`,
      "Accept-Encoding": "gzip, compress, deflate, br",
      host: domain,
      Connection: "close"
    },
    body: JSON.stringify({ refreshToken: rt })
  });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new KiroAuthError(`Social token refresh failed: ${res.status} ${body}`);
  }
  const data = await res.json();
  return {
    ...creds,
    accessToken: data.accessToken,
    refreshToken: data.refreshToken ?? creds.refreshToken,
    profileArn: data.profileArn ?? creds.profileArn,
    expiresAt: data.expiresIn ? new Date(Date.now() + data.expiresIn * 1000).toISOString() : creds.expiresAt
  };
}
async function refreshIdc(creds, config) {
  const rt = creds.refreshToken ?? "";
  const clientId = creds.clientId;
  const clientSecret = creds.clientSecret;
  if (!clientId || !clientSecret) {
    throw new KiroAuthError("IdC refresh requires clientId and clientSecret");
  }
  const region = effectiveAuthRegion(creds, config);
  const host = `oidc.${region}.amazonaws.com`;
  const url = `https://${host}/token`;
  const res = await pfetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Host: host,
      Connection: "keep-alive",
      "x-amz-user-agent": IDC_AMZ_USER_AGENT,
      Accept: "*/*",
      "Accept-Language": "*",
      "sec-fetch-mode": "cors",
      "User-Agent": "node",
      "Accept-Encoding": "br, gzip, deflate"
    },
    body: JSON.stringify({
      clientId,
      clientSecret,
      refreshToken: rt,
      grantType: "refresh_token"
    })
  });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new KiroAuthError(`IdC token refresh failed: ${res.status} ${body}`);
  }
  const data = await res.json();
  return {
    ...creds,
    accessToken: data.accessToken,
    refreshToken: data.refreshToken ?? creds.refreshToken,
    expiresAt: data.expiresIn ? new Date(Date.now() + data.expiresIn * 1000).toISOString() : creds.expiresAt
  };
}

// server/lib/kiro/multi-token-manager.ts
var OPTIONAL_TEXT_FIELDS = [
  "email",
  "displayName",
  "region",
  "authRegion",
  "apiRegion",
  "profileArn",
  "subscriptionTitle",
  "startUrl",
  "machineId",
  "proxyUrl",
  "proxyUsername"
];
var MAX_FAILURES_PER_CREDENTIAL = 3;
var STATS_SAVE_DEBOUNCE_MS = 30000;
var METER_PERSIST_DEBOUNCE_MS = 60000;
var USAGE_CACHE_PERSIST_DEBOUNCE_MS = 60000;
var SUSPICIOUS_THROTTLE_COOLDOWN_MS = 5 * 60000;
var RECOVERY_RETRY_MS = 30000;
var UPSTREAM_REQUEST_TIMEOUT_MS = 30000;
var MAX_PAGINATION_PAGES = 10;
var OVERAGE_BONUS_CREDITS = 1e4;
var FREE_TIER_BLOCKED_MODELS = new Set(["auto"]);
var DEFAULT_TIER_PRIORITY2 = ["free", "enterprise", "paid"];
var UNHEALTHY_DISABLED_REASONS = [
  "too_many_failures",
  "account_suspended"
];
var UNHEALTHY_DISABLED_REASON_SET = new Set(UNHEALTHY_DISABLED_REASONS);
function sha256Hex2(input) {
  return createHash2("sha256").update(input).digest("hex");
}
function isExpired(creds) {
  if (canonicalKiroAuthMethod(creds) === "api_key")
    return false;
  return isExpiringWithin(creds, 5) ?? true;
}
function isExpiringSoon(creds) {
  if (canonicalKiroAuthMethod(creds) === "api_key")
    return false;
  return isExpiringWithin(creds, 10) ?? false;
}
function dedupeSecret(creds) {
  const refreshToken = creds.refreshToken?.trim();
  if (refreshToken)
    return refreshToken;
  return apiKeyOf(creds);
}
function isExpiringWithin(creds, minutes) {
  if (!creds.expiresAt)
    return null;
  try {
    const t = new Date(creds.expiresAt).getTime();
    if (Number.isNaN(t))
      return null;
    return t <= Date.now() + minutes * 60000;
  } catch {
    return null;
  }
}
function classifyKiroCredentialTier(creds) {
  if (creds.profileArn || creds.startUrl)
    return "enterprise";
  const title = creds.subscriptionTitle;
  if (title) {
    const lower = title.toLowerCase();
    if (lower.includes("enterprise") || lower.includes("business"))
      return "enterprise";
    if (lower.includes("free"))
      return "free";
    return "paid";
  }
  if (canonicalKiroAuthMethod(creds) === "idc" && creds.clientId && creds.clientSecret) {
    return "enterprise";
  }
  return "free";
}
function applyOverageBonus(usageLimit, overageStatus) {
  return overageStatus === "ENABLED" ? usageLimit + OVERAGE_BONUS_CREDITS : usageLimit;
}
function parseDisabledAt(creds) {
  if (!creds.disabledAt)
    return 0;
  const ts = new Date(creds.disabledAt).getTime();
  return Number.isNaN(ts) ? 0 : ts;
}
function shouldPersistDisabledReason(reason) {
  return reason === "manual" || reason === "quota_exhausted" || reason === "account_suspended" || reason === "suspicious_throttle";
}
function isUnhealthyDisabledCredential(entry) {
  return entry.disabled && !!entry.disabledReason && UNHEALTHY_DISABLED_REASON_SET.has(entry.disabledReason);
}
function buildTierOrderMap(priority) {
  const map = {};
  const order = priority?.length ? priority : DEFAULT_TIER_PRIORITY2;
  for (let i = 0;i < order.length; i++)
    map[order[i]] = i;
  for (const tier of DEFAULT_TIER_PRIORITY2) {
    if (map[tier] == null)
      map[tier] = order.length;
  }
  return map;
}

class MultiTokenManager {
  config;
  entries;
  currentId;
  credentialsPath;
  isMultipleFormat;
  loadBalancingMode;
  lastStatsSaveAt = 0;
  statsDirty = false;
  refreshPromises = new Map;
  lastMeterPersistAt = 0;
  meterDirty = false;
  lastUsageCachePersistAt = 0;
  usageCacheDirty = false;
  throttleRecoveryTimers = new Map;
  usageCache = {};
  constructor(config, credentials, credentialsPath, isMultipleFormat) {
    this.config = config;
    this.credentialsPath = credentialsPath;
    this.isMultipleFormat = true;
    this.loadBalancingMode = config.loadBalancingMode ?? "priority";
    this.usageCache = loadUsageCache(credentialsPath);
    updateLimits(config.concurrency);
    updateIntervalConfig(config.requestInterval);
    let needsPersist = !isMultipleFormat;
    for (const c of credentials) {
      if (c.id != null && /^\d+$/.test(String(c.id))) {
        c.id = generateShortId();
        needsPersist = true;
      }
    }
    const seen = new Set;
    const deduped = [];
    for (const c of credentials) {
      const secret = dedupeSecret(c);
      if (secret) {
        const hash = sha256Hex2(secret);
        if (seen.has(hash))
          continue;
        seen.add(hash);
      }
      deduped.push(c);
    }
    this.entries = deduped.map((cred) => {
      if (!cred.id) {
        cred.id = generateShortId();
        needsPersist = true;
      }
      if (!cred.machineId) {
        const mid = generateMachineId(cred, config);
        if (mid) {
          cred.machineId = mid;
          needsPersist = true;
        }
      }
      const disabledState = this.restoreDisabledState(cred);
      if (disabledState.needsPersist)
        needsPersist = true;
      return {
        id: cred.id,
        credentials: cred,
        failureCount: disabledState.disabled ? MAX_FAILURES_PER_CREDENTIAL : 0,
        disabled: disabledState.disabled,
        disabledReason: disabledState.disabledReason,
        successCount: 0
      };
    });
    this.currentId = this.entries.length > 0 ? this.entries.reduce((best, entry) => this.comparePriority(entry, best)).id : "";
    this.recoverSuspiciousThrottlesOnStartup();
    if (needsPersist) {
      try {
        this.persistCredentials();
      } catch {}
    }
    this.restoreStats();
    process.on("beforeExit", () => {
      if (this.statsDirty)
        this.flushStats();
      if (this.meterDirty)
        this.persistCredentials();
      if (this.usageCacheDirty)
        this.flushUsageCache();
    });
  }
  get maxRetries() {
    return Math.max(1, Math.min(this.entries.length * 3, 9));
  }
  async acquireContext(model, excludeIds) {
    const total = this.entries.length;
    const triedIds = new Set(excludeIds);
    while (triedIds.size < total) {
      const entry = this.selectEntry(model ?? undefined, triedIds);
      if (!entry) {
        if (this.trySelfHeal())
          continue;
        break;
      }
      const ctx = await this.tryEnsureToken(entry);
      if (ctx)
        return ctx;
      triedIds.add(entry.id);
    }
    throw new KiroAuthError(`All credentials exhausted (available: ${this.availableCount}/${total})`);
  }
  selectEntry(model, excludeIds) {
    const available = this.entries.filter((entry) => {
      if (entry.disabled)
        return false;
      if (excludeIds?.has(entry.id))
        return false;
      return true;
    });
    let candidates = available.filter((entry) => this.supportsModel(entry, model));
    if (candidates.length === 0 && model && !FREE_TIER_BLOCKED_MODELS.has(model)) {
      candidates = available.filter((entry) => classifyKiroCredentialTier(entry.credentials) === "free");
    }
    if (candidates.length === 0)
      return null;
    if (this.config.ignoreTierPriority)
      return this.pickWithinGroup(candidates);
    const tierOrderMap = buildTierOrderMap(this.config.tierPriority);
    const ordered = [...candidates].sort((a, b) => {
      const tierDiff = tierOrderMap[classifyKiroCredentialTier(a.credentials)] - tierOrderMap[classifyKiroCredentialTier(b.credentials)];
      if (tierDiff !== 0)
        return tierDiff;
      return this.scoreWithinGroup(a, b);
    });
    return ordered[0] ?? null;
  }
  supportsModel(entry, model) {
    if (!model)
      return true;
    if (classifyKiroCredentialTier(entry.credentials) === "free" && FREE_TIER_BLOCKED_MODELS.has(model)) {
      return false;
    }
    const models = entry.credentials.availableModels;
    return !models?.length || models.includes(model);
  }
  pickWithinGroup(entries) {
    return entries.reduce((best, entry) => this.scoreWithinGroup(entry, best) < 0 ? entry : best);
  }
  scoreWithinGroup(a, b) {
    if (this.loadBalancingMode === "priority") {
      return (a.credentials.priority ?? 0) - (b.credentials.priority ?? 0);
    }
    const aHeadroom = getHeadroom(a.id) > 0 ? 1 : 0;
    const bHeadroom = getHeadroom(b.id) > 0 ? 1 : 0;
    if (aHeadroom !== bHeadroom)
      return bHeadroom - aHeadroom;
    const rpmDiff = getWindowRpm(a.id) - getWindowRpm(b.id);
    if (rpmDiff !== 0)
      return rpmDiff;
    const usageDiff = this.currentUsage(a) - this.currentUsage(b);
    if (usageDiff !== 0)
      return usageDiff;
    return (a.credentials.priority ?? 0) - (b.credentials.priority ?? 0);
  }
  comparePriority(a, b) {
    const tierOrderMap = buildTierOrderMap(this.config.tierPriority);
    const tierDiff = tierOrderMap[classifyKiroCredentialTier(a.credentials)] - tierOrderMap[classifyKiroCredentialTier(b.credentials)];
    if (tierDiff < 0)
      return a;
    if (tierDiff > 0)
      return b;
    return (a.credentials.priority ?? 0) < (b.credentials.priority ?? 0) ? a : b;
  }
  currentUsage(entry) {
    const meterUsage = entry.credentials.meterUsage ?? 0;
    const cached = this.usageCache[entry.id];
    if (!cached)
      return meterUsage;
    if (this.isUsageCacheExpired(cached))
      return meterUsage;
    return Math.max(cached.currentUsage, meterUsage);
  }
  isUsageCacheExpired(entry) {
    if (!entry.nextResetAt)
      return false;
    const resetAt = entry.nextResetAt < 10000000000 ? entry.nextResetAt * 1000 : entry.nextResetAt;
    return resetAt <= Date.now();
  }
  async tryEnsureToken(entry) {
    const creds = entry.credentials;
    if (canonicalKiroAuthMethod(creds) === "api_key") {
      const key = apiKeyOf(creds);
      if (!key) {
        logger.warn("[kiro] api_key credential missing stored key", { credentialId: entry.id });
        return null;
      }
      this.currentId = entry.id;
      return { id: entry.id, credentials: creds, token: key };
    }
    const needsRefresh = isExpired(creds) || isExpiringSoon(creds);
    if (!needsRefresh) {
      if (!creds.accessToken)
        return null;
      this.currentId = entry.id;
      return { id: entry.id, credentials: creds, token: creds.accessToken };
    }
    try {
      const refreshed = await this.deduplicatedRefresh(entry.id, creds);
      entry.credentials = refreshed;
      if (isExpired(refreshed))
        return null;
      if (!refreshed.accessToken)
        return null;
      if (this.isMultipleFormat) {
        try {
          this.persistCredentials();
        } catch {}
      }
      this.currentId = entry.id;
      return {
        id: entry.id,
        credentials: refreshed,
        token: refreshed.accessToken
      };
    } catch (err) {
      logger.warn("[kiro] token refresh failed", { credentialId: entry.id, error: String(err) });
      return null;
    }
  }
  deduplicatedRefresh(id, creds) {
    const existing = this.refreshPromises.get(id);
    if (existing)
      return existing;
    const promise = refreshCredentials(creds, this.config).finally(() => {
      this.refreshPromises.delete(id);
    });
    this.refreshPromises.set(id, promise);
    return promise;
  }
  reportSuccess(id) {
    const entry = this.entries.find((e) => e.id === id);
    if (!entry)
      return;
    entry.failureCount = 0;
    entry.successCount++;
    entry.lastUsedAt = new Date().toISOString();
    this.saveStatsDebounced();
  }
  reportFailure(id, reason) {
    const entry = this.entries.find((e) => e.id === id);
    if (!entry)
      return this.entries.some((e) => !e.disabled);
    if (reason === "quota_exhausted")
      return this.reportQuotaExhausted(id);
    if (reason === "account_suspended") {
      this.disableEntry(entry, "account_suspended", true);
      return this.entries.some((e) => !e.disabled);
    }
    if (reason === "suspicious_throttle")
      return this.reportSuspiciousThrottle(id);
    entry.failureCount++;
    entry.lastUsedAt = new Date().toISOString();
    if (entry.failureCount >= MAX_FAILURES_PER_CREDENTIAL) {
      this.disableEntry(entry, "too_many_failures", false);
    }
    this.saveStatsDebounced();
    return this.entries.some((e) => !e.disabled);
  }
  reportQuotaExhausted(id) {
    const entry = this.entries.find((e) => e.id === id);
    if (!entry)
      return this.entries.some((e) => !e.disabled);
    this.disableEntry(entry, "quota_exhausted", true);
    return this.entries.some((e) => !e.disabled);
  }
  reportSuspiciousThrottle(id, excludeIds) {
    const entry = this.entries.find((e) => e.id === id);
    if (!entry)
      return this.entries.some((e) => !e.disabled);
    if (!this.hasAlternativeAvailableCredential(id, excludeIds)) {
      logger.warn("[kiro] last available credential suspicious-throttled, retrying", {
        credentialId: id
      });
      entry.lastUsedAt = new Date().toISOString();
      this.saveStatsDebounced();
      return true;
    }
    logger.warn("[kiro] credential suspicious-throttled, cooling down", { credentialId: id });
    this.disableEntry(entry, "suspicious_throttle", true);
    this.scheduleThrottleRecovery(id, SUSPICIOUS_THROTTLE_COOLDOWN_MS);
    return this.entries.some((e) => !e.disabled);
  }
  hasAlternativeAvailableCredential(id, excludeIds) {
    return this.entries.some((e) => e.id !== id && !e.disabled && !excludeIds?.has(e.id));
  }
  disableEntry(entry, reason, persist) {
    entry.disabled = true;
    entry.disabledReason = reason;
    entry.failureCount = MAX_FAILURES_PER_CREDENTIAL;
    entry.lastUsedAt = new Date().toISOString();
    if (persist && shouldPersistDisabledReason(reason)) {
      entry.credentials.disabled = true;
      entry.credentials.disabledReason = reason;
      entry.credentials.disabledAt = new Date().toISOString();
      this.persistCredentials();
    }
    this.saveStatsDebounced();
  }
  trySelfHeal() {
    const hasTooManyFailures = this.entries.some((e) => e.disabled && e.disabledReason === "too_many_failures");
    if (!hasTooManyFailures)
      return false;
    for (const e of this.entries) {
      if (e.disabledReason === "too_many_failures") {
        e.disabled = false;
        e.disabledReason = undefined;
        e.failureCount = 0;
      }
    }
    return true;
  }
  restoreDisabledState(creds) {
    if (!creds.disabled || !creds.disabledReason) {
      return { disabled: false, needsPersist: false };
    }
    if (creds.disabledReason === "suspicious_throttle") {
      const disabledAt = parseDisabledAt(creds);
      const elapsed = disabledAt > 0 ? Date.now() - disabledAt : SUSPICIOUS_THROTTLE_COOLDOWN_MS;
      if (elapsed < SUSPICIOUS_THROTTLE_COOLDOWN_MS) {
        return { disabled: true, disabledReason: "suspicious_throttle", needsPersist: false };
      }
      creds.disabled = undefined;
      creds.disabledReason = undefined;
      creds.disabledAt = undefined;
      return { disabled: false, needsPersist: true };
    }
    if (shouldPersistDisabledReason(creds.disabledReason)) {
      return { disabled: true, disabledReason: creds.disabledReason, needsPersist: false };
    }
    creds.disabled = undefined;
    creds.disabledReason = undefined;
    creds.disabledAt = undefined;
    return { disabled: false, needsPersist: true };
  }
  recoverSuspiciousThrottlesOnStartup() {
    for (const entry of this.entries) {
      if (entry.disabledReason !== "suspicious_throttle")
        continue;
      const disabledAt = parseDisabledAt(entry.credentials);
      const elapsed = disabledAt > 0 ? Date.now() - disabledAt : 0;
      const remaining = Math.max(0, SUSPICIOUS_THROTTLE_COOLDOWN_MS - elapsed);
      this.scheduleThrottleRecovery(entry.id, remaining);
    }
  }
  scheduleThrottleRecovery(id, delayMs) {
    if (this.throttleRecoveryTimers.has(id))
      return;
    const timer = setTimeout(() => {
      this.throttleRecoveryTimers.delete(id);
      try {
        this.recoverSuspiciousThrottleCredential(id);
      } catch (err) {
        logger.error("[kiro] failed to recover suspicious-throttled credential", {
          credentialId: id,
          error: String(err)
        });
        this.scheduleThrottleRecovery(id, RECOVERY_RETRY_MS);
      }
    }, delayMs);
    this.throttleRecoveryTimers.set(id, timer);
  }
  recoverSuspiciousThrottleCredential(id) {
    const entry = this.entries.find((e) => e.id === id);
    if (!entry || entry.disabledReason !== "suspicious_throttle")
      return;
    entry.disabled = false;
    entry.disabledReason = undefined;
    entry.failureCount = 0;
    entry.credentials.disabled = undefined;
    entry.credentials.disabledReason = undefined;
    entry.credentials.disabledAt = undefined;
    this.persistCredentials();
    logger.info("[kiro] suspicious-throttled credential recovered", { credentialId: id });
  }
  snapshot(opts) {
    const available = this.entries.filter((e) => !e.disabled).length;
    const unhealthyTotal = this.entries.filter(isUnhealthyDisabledCredential).length;
    const paid = this.entries.filter((entry) => classifyKiroCredentialTier(entry.credentials) === "paid");
    const enterprise = this.entries.filter((entry) => classifyKiroCredentialTier(entry.credentials) === "enterprise");
    const free = this.entries.filter((entry) => classifyKiroCredentialTier(entry.credentials) === "free");
    const pageSize = opts?.pageSize ?? 0;
    const isPaged = pageSize > 0;
    const slicePage = (entries2, page) => {
      if (!isPaged || !page || page < 1)
        return entries2;
      const maxPage = Math.max(1, Math.ceil(entries2.length / pageSize));
      const safePage = Math.min(page, maxPage);
      const start = (safePage - 1) * pageSize;
      return entries2.slice(start, start + pageSize);
    };
    const mapEntry = (entry) => ({
      id: entry.id,
      priority: entry.credentials.priority ?? 0,
      disabled: entry.disabled,
      disabledReason: entry.disabledReason,
      failureCount: entry.failureCount,
      successCount: entry.successCount,
      lastUsedAt: entry.lastUsedAt,
      email: entry.credentials.email,
      displayName: entry.credentials.displayName,
      hasProfileArn: !!entry.credentials.profileArn,
      hasStartUrl: !!entry.credentials.startUrl,
      expiresAt: entry.credentials.expiresAt,
      authMethod: entry.credentials.authMethod,
      isApiKey: isApiKeyCredential(entry.credentials),
      subscriptionTitle: entry.credentials.subscriptionTitle,
      region: entry.credentials.region,
      authRegion: entry.credentials.authRegion,
      apiRegion: entry.credentials.apiRegion,
      profileArn: entry.credentials.profileArn,
      startUrl: entry.credentials.startUrl,
      machineId: entry.credentials.machineId,
      hasClientId: !!entry.credentials.clientId,
      hasClientSecret: !!entry.credentials.clientSecret,
      proxyUrl: entry.credentials.proxyUrl,
      proxyUsername: entry.credentials.proxyUsername,
      hasProxyPassword: !!entry.credentials.proxyPassword,
      meterUsage: entry.credentials.meterUsage,
      availableModels: entry.credentials.availableModels,
      tier: classifyKiroCredentialTier(entry.credentials)
    });
    const paidEntries = slicePage(paid, opts?.paidPage).map(mapEntry);
    const enterpriseEntries = slicePage(enterprise, opts?.enterprisePage).map(mapEntry);
    const freeEntries = slicePage(free, opts?.freePage).map(mapEntry);
    const entries = isPaged ? [...paidEntries, ...enterpriseEntries, ...freeEntries] : this.entries.map(mapEntry);
    return {
      entries,
      paidEntries,
      enterpriseEntries,
      freeEntries,
      paidTotal: paid.length,
      enterpriseTotal: enterprise.length,
      freeTotal: free.length,
      unhealthyTotal,
      currentId: this.currentId,
      loadBalancingMode: this.loadBalancingMode,
      total: this.entries.length,
      available,
      concurrencyStates: getAllStates(),
      rpmCounts: getRpmCounts(),
      throttleCounts: getThrottleCounts()
    };
  }
  setLoadBalancingMode(mode) {
    this.loadBalancingMode = mode;
    this.config.loadBalancingMode = mode;
  }
  setDisabled(id, disabled) {
    const entry = this.entries.find((e) => e.id === id);
    if (!entry)
      throw new Error(`Credential not found: ${id}`);
    if (!disabled) {
      this.clearRecoveryTimer(id);
      entry.failureCount = 0;
      entry.disabled = false;
      entry.disabledReason = undefined;
      entry.credentials.disabled = undefined;
      entry.credentials.disabledReason = undefined;
      entry.credentials.disabledAt = undefined;
    } else {
      this.disableEntry(entry, "manual", true);
      return;
    }
    this.persistCredentials();
  }
  setPriority(id, priority) {
    const entry = this.entries.find((e) => e.id === id);
    if (!entry)
      throw new Error(`Credential not found: ${id}`);
    entry.credentials.priority = priority;
    this.persistCredentials();
  }
  resetAndEnable(id) {
    const entry = this.entries.find((e) => e.id === id);
    if (!entry)
      throw new Error(`Credential not found: ${id}`);
    this.clearRecoveryTimer(id);
    entry.failureCount = 0;
    entry.disabled = false;
    entry.disabledReason = undefined;
    entry.credentials.disabled = undefined;
    entry.credentials.disabledReason = undefined;
    entry.credentials.disabledAt = undefined;
    this.persistCredentials();
  }
  removeCredential(id) {
    const result = this.removeCredentials([id]);
    if (result.notFound.length > 0)
      throw new Error(`Credential not found: ${id}`);
  }
  removeCredentials(ids) {
    const uniqueIds = [...new Set(ids)];
    const existingIds = new Set(this.entries.map((entry) => entry.id));
    const removed = uniqueIds.filter((id) => existingIds.has(id));
    const notFound = uniqueIds.filter((id) => !existingIds.has(id));
    if (removed.length === 0)
      return { removed, notFound };
    const removedIds = new Set(removed);
    for (const id of removed) {
      this.clearRecoveryTimer(id);
      this.refreshPromises.delete(id);
      delete this.usageCache[id];
    }
    this.entries = this.entries.filter((entry) => !removedIds.has(entry.id));
    if (!this.entries.some((entry) => entry.id === this.currentId)) {
      this.currentId = this.entries.length ? this.entries.reduce((best, entry) => this.comparePriority(entry, best)).id : "";
    }
    this.persistCredentials();
    this.flushStats();
    this.flushUsageCache();
    return { removed, notFound };
  }
  removeUnhealthyCredentials() {
    const ids = this.entries.filter(isUnhealthyDisabledCredential).map((entry) => entry.id);
    const result = this.removeCredentials(ids);
    return { removed: result.removed, reasons: [...UNHEALTHY_DISABLED_REASONS] };
  }
  clearRecoveryTimer(id) {
    const timer = this.throttleRecoveryTimers.get(id);
    if (!timer)
      return;
    clearTimeout(timer);
    this.throttleRecoveryTimers.delete(id);
  }
  async manualRefresh(id) {
    const entry = this.entries.find((e) => e.id === id);
    if (!entry)
      throw new Error(`Credential not found: ${id}`);
    if (canonicalKiroAuthMethod(entry.credentials) === "api_key") {
      throw new Error("Static API key credentials are never refreshed");
    }
    const refreshed = await this.deduplicatedRefresh(id, entry.credentials);
    entry.credentials = refreshed;
    this.persistCredentials();
  }
  updateCredential(id, fields) {
    const entry = this.entries.find((e) => e.id === id);
    if (!entry)
      throw new Error(`Credential not found: ${id}`);
    const next = { ...entry.credentials };
    for (const key of OPTIONAL_TEXT_FIELDS) {
      const value = fields[key];
      if (value === undefined)
        continue;
      const trimmed = value.trim();
      if (trimmed)
        next[key] = trimmed;
      else
        delete next[key];
    }
    const refreshToken = fields.refreshToken?.trim();
    if (refreshToken)
      next.refreshToken = refreshToken;
    const apiKey = fields.apiKey?.trim();
    const clientId = fields.clientId?.trim();
    if (clientId)
      next.clientId = clientId;
    const clientSecret = fields.clientSecret?.trim();
    if (clientSecret)
      next.clientSecret = clientSecret;
    const proxyPassword = fields.proxyPassword?.trim();
    if (proxyPassword)
      next.proxyPassword = proxyPassword;
    if (fields.priority !== undefined)
      next.priority = fields.priority;
    const requestedAuthMethod = canonicalizeAuthMethodValue(fields.authMethod ?? next.authMethod);
    const becomesApiKey = apiKey ? true : requestedAuthMethod === "api_key";
    if (becomesApiKey) {
      const key = apiKey ?? apiKeyOf(entry.credentials);
      if (!key) {
        throw new Error("apiKey is required when switching to api_key credentials");
      }
      next.authMethod = "api_key";
      next.apiKey = key;
      next.accessToken = key;
      delete next.expiresAt;
    } else {
      if (isApiKeyCredential(entry.credentials) && !refreshToken) {
        throw new Error("refreshToken is required when switching api_key credentials to OAuth");
      }
      if (refreshToken) {
        delete next.accessToken;
        delete next.expiresAt;
      }
      if (!next.refreshToken?.trim()) {
        throw new Error("refreshToken is required for non-api_key credentials");
      }
      if (isApiKeyCredential(entry.credentials))
        delete next.apiKey;
      next.authMethod = requestedAuthMethod ?? canonicalKiroAuthMethod(next);
    }
    const nextSecret = dedupeSecret(next);
    if (!nextSecret)
      throw new Error("Credential must carry a refreshToken or apiKey");
    const nextHash = sha256Hex2(nextSecret);
    for (const other of this.entries) {
      if (other.id === id)
        continue;
      const secret = dedupeSecret(other.credentials);
      if (secret && sha256Hex2(secret) === nextHash) {
        throw new Error(`Credential secret already used by ${other.id}`);
      }
    }
    if (!fields.machineId?.trim() && dedupeSecret(entry.credentials) !== nextSecret) {
      const derivedFromOld = generateMachineId(entry.credentials, this.config);
      if (next.machineId && next.machineId === derivedFromOld)
        delete next.machineId;
    }
    if (!next.machineId) {
      const mid = generateMachineId(next, this.config);
      if (mid)
        next.machineId = mid;
    }
    entry.credentials = next;
    this.persistCredentials();
  }
  addCredentials(newCreds) {
    const existingHashes = new Set;
    for (const e of this.entries) {
      const secret = dedupeSecret(e.credentials);
      if (secret)
        existingHashes.add(sha256Hex2(secret));
    }
    let added = 0;
    let duplicates = 0;
    for (const cred of newCreds) {
      const secret = dedupeSecret(cred);
      if (!secret) {
        duplicates++;
        continue;
      }
      const hash = sha256Hex2(secret);
      if (existingHashes.has(hash)) {
        duplicates++;
        continue;
      }
      existingHashes.add(hash);
      cred.id = generateShortId();
      cred.disabled = undefined;
      cred.disabledReason = undefined;
      cred.disabledAt = undefined;
      if (!cred.machineId) {
        const mid = generateMachineId(cred, this.config);
        if (mid)
          cred.machineId = mid;
      }
      this.entries.push({
        id: cred.id,
        credentials: cred,
        failureCount: 0,
        disabled: false,
        successCount: 0
      });
      added++;
    }
    if (added > 0) {
      this.isMultipleFormat = true;
      this.persistCredentials();
    }
    return { added, duplicates };
  }
  async getUsageLimits(id) {
    logger.debug("[kiro:usage] start", { credentialId: id });
    const entry = this.entries.find((e) => e.id === id);
    if (!entry)
      throw new Error(`Credential not found: ${id}`);
    logger.debug("[kiro:usage] tryEnsureToken");
    const ctx = await this.tryEnsureToken(entry);
    if (!ctx)
      throw new KiroAuthError(`Cannot acquire valid token for credential ${id}`);
    logger.debug("[kiro:usage] token ok");
    const creds = entry.credentials;
    const region = effectiveApiRegion(this.config, creds);
    const { kiroVersion } = this.config;
    const reqBody = {
      origin: "AI_EDITOR",
      resourceType: "AGENTIC_REQUEST"
    };
    if (creds.profileArn) {
      reqBody.profileArn = creds.profileArn;
    }
    const requestUsage = async (usageRegion) => {
      const host = `q.${usageRegion}.amazonaws.com`;
      const url = `https://${host}`;
      const headers = {
        "Content-Type": "application/x-amz-json-1.0",
        "x-amz-target": "AmazonCodeWhispererService.GetUsageLimits",
        "User-Agent": `AmazonQ-For-CLI/Version/${kiroVersion}`,
        Host: host,
        "amz-sdk-invocation-id": randomUUID(),
        "amz-sdk-request": "attempt=1; max=1",
        Authorization: `Bearer ${ctx.token}`
      };
      applyKiroTokenTypeHeader(headers, creds);
      logger.debug("[kiro:usage] fetch POST", { url });
      let res2;
      try {
        res2 = await pfetch(url, {
          method: "POST",
          headers,
          body: JSON.stringify(reqBody),
          signal: AbortSignal.timeout(UPSTREAM_REQUEST_TIMEOUT_MS)
        });
      } catch (err) {
        logger.error("[kiro:usage] fetch threw", { error: String(err) });
        throw new Error(`getUsageLimits network error: ${err instanceof Error ? err.message : String(err)}`);
      }
      logger.debug("[kiro:usage] response", { status: res2.status });
      if (!res2.ok) {
        const body = await res2.text().catch(() => "");
        logger.warn("[kiro:usage] not ok", { status: res2.status, body: body.slice(0, 200) });
        throw new Error(`getUsageLimits failed: ${res2.status} ${body}`);
      }
      return res2;
    };
    let res;
    try {
      res = await requestUsage(region);
    } catch (err) {
      if (region === DEFAULT_KIRO_REGION)
        throw err;
      logger.warn("[kiro:usage] region failed, retrying default region", {
        credentialId: id,
        region,
        error: String(err)
      });
      try {
        res = await requestUsage(DEFAULT_KIRO_REGION);
      } catch (fallbackErr) {
        throw new Error(`getUsageLimits failed for region ${region} (${err instanceof Error ? err.message : String(err)}) and ${DEFAULT_KIRO_REGION} fallback: ${fallbackErr instanceof Error ? fallbackErr.message : String(fallbackErr)}`);
      }
    }
    logger.debug("[kiro:usage] parsing json");
    let data;
    try {
      data = await res.json();
    } catch {
      throw new Error("getUsageLimits: failed to parse response JSON");
    }
    logger.debug("[kiro:usage] json ok", { breakdowns: data.usageBreakdownList?.length ?? 0 });
    const breakdown = data.usageBreakdownList?.[0];
    let totalUsage = breakdown?.currentUsageWithPrecision ?? breakdown?.currentUsage ?? 0;
    let totalLimit = breakdown?.usageLimitWithPrecision ?? breakdown?.usageLimit ?? 0;
    if (breakdown?.freeTrialInfo?.freeTrialStatus === "ACTIVE") {
      totalUsage += breakdown.freeTrialInfo.currentUsageWithPrecision ?? breakdown.freeTrialInfo.currentUsage ?? 0;
      totalLimit += breakdown.freeTrialInfo.usageLimitWithPrecision ?? breakdown.freeTrialInfo.usageLimit ?? 0;
    }
    for (const bonus of breakdown?.bonuses ?? []) {
      if (bonus.status === "ACTIVE") {
        totalUsage += bonus.currentUsage ?? 0;
        totalLimit += bonus.usageLimit ?? 0;
      }
    }
    const subscriptionTitle = data.subscriptionInfo?.subscriptionTitle;
    const nextResetAt = data.nextDateReset ?? breakdown?.nextDateReset;
    const overageStatus = data.overageConfiguration?.overageStatus;
    const effectiveLimit = applyOverageBonus(totalLimit, overageStatus);
    logger.debug("[kiro:usage] parsed", { totalUsage, totalLimit, subscriptionTitle });
    let shouldPersist = false;
    if (subscriptionTitle && subscriptionTitle !== creds.subscriptionTitle) {
      creds.subscriptionTitle = subscriptionTitle;
      shouldPersist = true;
    }
    if (creds.meterUsage !== totalUsage) {
      creds.meterUsage = totalUsage;
      shouldPersist = true;
    }
    if (shouldPersist)
      this.persistCredentials();
    const remaining = Math.max(0, effectiveLimit - totalUsage);
    const usagePercentage = effectiveLimit > 0 ? Math.round(totalUsage / effectiveLimit * 1e4) / 100 : 0;
    const result = {
      id,
      subscriptionTitle,
      currentUsage: totalUsage,
      usageLimit: effectiveLimit,
      remaining,
      usagePercentage,
      nextResetAt,
      queriedAt: new Date().toISOString(),
      overageStatus,
      overageCap: breakdown?.overageCap,
      overageRate: breakdown?.overageRate,
      overageCharges: breakdown?.overageCharges,
      currentOverages: breakdown?.currentOverages
    };
    this.usageCache[id] = result;
    this.flushUsageCache();
    logger.debug("[kiro:usage] done", { remaining });
    return result;
  }
  async listAvailableModels(credentialId) {
    const entry = credentialId ? this.entries.find((e) => e.id === credentialId) : this.selectEntry();
    if (!entry)
      throw new Error(credentialId ? `Credential not found: ${credentialId}` : "No available credentials");
    const ctx = await this.tryEnsureToken(entry);
    if (!ctx)
      throw new KiroAuthError(`Cannot acquire valid token for credential ${entry.id}`);
    const creds = entry.credentials;
    const region = effectiveApiRegion(this.config, creds);
    const { kiroVersion } = this.config;
    const host = `q.${region}.amazonaws.com`;
    const url = `https://${host}`;
    const reqBody = {
      origin: "AI_EDITOR",
      maxResults: 100
    };
    if (creds.profileArn)
      reqBody.profileArn = creds.profileArn;
    const allModels = [];
    let nextToken;
    let pageCount = 0;
    logger.debug("[kiro:models] start", { credentialId: entry.id, region });
    do {
      if (nextToken)
        reqBody.nextToken = nextToken;
      else
        delete reqBody.nextToken;
      const params = new URLSearchParams;
      params.set("origin", "AI_EDITOR");
      if (reqBody.maxResults != null)
        params.set("maxResults", String(reqBody.maxResults));
      if (creds.profileArn)
        params.set("profileArn", creds.profileArn);
      if (nextToken)
        params.set("nextToken", nextToken);
      const requestUrl = `${url}/?${params.toString()}`;
      const headers = {
        "Content-Type": "application/x-amz-json-1.0",
        "x-amz-target": "AmazonCodeWhispererService.ListAvailableModels",
        "User-Agent": `AmazonQ-For-CLI/Version/${kiroVersion}`,
        Host: host,
        "amz-sdk-invocation-id": randomUUID(),
        "amz-sdk-request": "attempt=1; max=1",
        Authorization: `Bearer ${ctx.token}`
      };
      applyKiroTokenTypeHeader(headers, creds);
      logger.debug("[kiro:models] fetch POST", { url: requestUrl });
      let res;
      try {
        res = await pfetch(requestUrl, {
          method: "POST",
          headers,
          body: JSON.stringify(reqBody),
          signal: AbortSignal.timeout(UPSTREAM_REQUEST_TIMEOUT_MS)
        });
      } catch (err) {
        throw new Error(`ListAvailableModels network error: ${err instanceof Error ? err.message : String(err)}`);
      }
      logger.debug("[kiro:models] response", { status: res.status });
      if (!res.ok) {
        const body = await res.text().catch(() => "");
        logger.warn("[kiro:models] not ok", { status: res.status, body: body.slice(0, 500) });
        throw new Error(`ListAvailableModels failed: ${res.status} ${body}`);
      }
      let data;
      try {
        data = await res.json();
      } catch {
        throw new Error("ListAvailableModels: failed to parse response JSON");
      }
      logger.debug("[kiro:models] json ok", {
        models: Array.isArray(data.models) ? data.models.length : typeof data.models,
        keys: Object.keys(data).join(",")
      });
      if (data.models != null && !Array.isArray(data.models)) {
        throw new Error(`ListAvailableModels: unexpected response shape: ${JSON.stringify(data).slice(0, 500)}`);
      }
      if (!data.models) {
        throw new Error(`ListAvailableModels: response missing 'models' field: ${JSON.stringify(data).slice(0, 500)}`);
      }
      if (Array.isArray(data.models))
        allModels.push(...data.models);
      nextToken = data.nextToken || undefined;
      pageCount++;
      if (nextToken && pageCount >= MAX_PAGINATION_PAGES) {
        logger.warn("[kiro:models] pagination limit reached, truncating", {
          credentialId: entry.id,
          pageCount,
          totalModelsSoFar: allModels.length
        });
        break;
      }
    } while (nextToken);
    logger.debug("[kiro:models] done", { total: allModels.length });
    return { models: allModels, credentialId: entry.id };
  }
  async fetchAndStoreModels(credentialId) {
    const result = await this.listAvailableModels(credentialId);
    const modelIds = result.models.map((m) => String(m.model_id ?? m.modelId ?? "")).filter(Boolean);
    const entry = this.entries.find((e) => e.id === credentialId);
    if (entry) {
      entry.credentials.availableModels = modelIds;
      this.persistCredentials();
    }
    return modelIds;
  }
  async fetchAllCredentialModels() {
    const seen = new Set;
    const allModels = [];
    for (const entry of this.entries) {
      if (entry.disabled)
        continue;
      try {
        const result = await this.listAvailableModels(entry.id);
        const modelIds = [];
        for (const m of result.models) {
          const id = String(m.model_id ?? m.modelId ?? "");
          if (!id)
            continue;
          modelIds.push(id);
          if (!seen.has(id)) {
            seen.add(id);
            allModels.push(m);
          }
        }
        entry.credentials.availableModels = modelIds;
      } catch (err) {
        logger.warn("[kiro:models] failed for credential", {
          credentialId: entry.id,
          error: String(err)
        });
      }
    }
    this.persistCredentials();
    return allModels;
  }
  get availableCount() {
    return this.entries.filter((e) => !e.disabled).length;
  }
  get currentConfig() {
    return this.config;
  }
  restoreStats() {
    try {
      const stats = loadStats(this.credentialsPath);
      for (const entry of this.entries) {
        const s = stats[String(entry.id)];
        if (s) {
          entry.successCount = s.successCount ?? 0;
          entry.lastUsedAt = s.lastUsedAt;
        }
      }
      this.lastStatsSaveAt = Date.now();
    } catch {}
  }
  saveStatsDebounced() {
    this.statsDirty = true;
    if (Date.now() - this.lastStatsSaveAt > STATS_SAVE_DEBOUNCE_MS) {
      this.flushStats();
    }
  }
  flushStats() {
    try {
      const stats = {};
      for (const e of this.entries) {
        stats[String(e.id)] = {
          successCount: e.successCount,
          lastUsedAt: e.lastUsedAt
        };
      }
      saveStats(this.credentialsPath, stats);
      this.lastStatsSaveAt = Date.now();
      this.statsDirty = false;
    } catch {}
  }
  saveUsageCacheDebounced() {
    this.usageCacheDirty = true;
    if (Date.now() - this.lastUsageCachePersistAt > USAGE_CACHE_PERSIST_DEBOUNCE_MS) {
      this.flushUsageCache();
    }
  }
  flushUsageCache() {
    try {
      saveUsageCache(this.credentialsPath, this.usageCache);
      this.lastUsageCachePersistAt = Date.now();
      this.usageCacheDirty = false;
    } catch {}
  }
  persistCredentials() {
    saveAllCredentials(this.credentialsPath, this.entries.map((e) => e.credentials));
  }
  addMeterUsage(credentialId, amount) {
    if (amount <= 0)
      return;
    const entry = this.entries.find((e) => e.id === credentialId);
    if (!entry)
      return;
    entry.credentials.meterUsage = (entry.credentials.meterUsage ?? 0) + amount;
    const cached = this.usageCache[credentialId];
    if (cached) {
      cached.currentUsage = Math.max(cached.currentUsage, entry.credentials.meterUsage);
      cached.remaining = Math.max(0, cached.usageLimit - cached.currentUsage);
      cached.usagePercentage = cached.usageLimit > 0 ? Math.round(cached.currentUsage / cached.usageLimit * 1e4) / 100 : 0;
      cached.queriedAt = new Date().toISOString();
      this.saveUsageCacheDebounced();
    }
    this.meterDirty = true;
    if (Date.now() - this.lastMeterPersistAt > METER_PERSIST_DEBOUNCE_MS) {
      this.persistCredentials();
      this.lastMeterPersistAt = Date.now();
      this.meterDirty = false;
    }
  }
  setMeterUsage(credentialId, value) {
    const entry = this.entries.find((e) => e.id === credentialId);
    if (!entry)
      return;
    entry.credentials.meterUsage = value;
    const cached = this.usageCache[credentialId];
    if (cached) {
      cached.currentUsage = value;
      cached.remaining = Math.max(0, cached.usageLimit - value);
      cached.usagePercentage = cached.usageLimit > 0 ? Math.round(value / cached.usageLimit * 1e4) / 100 : 0;
      cached.queriedAt = new Date().toISOString();
      this.flushUsageCache();
    }
    this.persistCredentials();
    this.lastMeterPersistAt = Date.now();
    this.meterDirty = false;
  }
}

// server/lib/kiro/quota-check.ts
function parseJsonObject(body) {
  try {
    const parsed = JSON.parse(body);
    return parsed && typeof parsed === "object" ? parsed : null;
  } catch {
    return null;
  }
}
function nestedReason(json) {
  const reason = json?.reason;
  if (typeof reason === "string")
    return reason;
  const error = json?.error;
  if (error && typeof error === "object") {
    const nested = error.reason;
    if (typeof nested === "string")
      return nested;
  }
  return;
}
function isMonthlyRequestLimit(body) {
  if (body.includes("MONTHLY_REQUEST_COUNT"))
    return true;
  if (body.includes("OVERAGE_REQUEST_LIMIT_EXCEEDED"))
    return true;
  const json = parseJsonObject(body);
  const reason = nestedReason(json);
  if (reason === "MONTHLY_REQUEST_COUNT")
    return true;
  if (reason === "OVERAGE_REQUEST_LIMIT_EXCEEDED")
    return true;
  const type = json?.__type;
  return type != null && String(type).includes("ServiceQuotaExceededException") && (reason === "MONTHLY_REQUEST_COUNT" || reason === "OVERAGE_REQUEST_LIMIT_EXCEEDED");
}
function isContextLengthExceeded(body) {
  if (body.includes("CONTENT_LENGTH_EXCEEDS_THRESHOLD"))
    return true;
  const reason = nestedReason(parseJsonObject(body));
  return reason === "CONTENT_LENGTH_EXCEEDS_THRESHOLD";
}
var SUSPENDED_PROSE_RE = /temporarily\s+is\s+suspended|is\s+temporarily\s+suspended|temporarily\s+suspended|locked\s+your\s+account/i;
var SUSPENDED_EXCEPTION_TYPE_RE = /AccessDeniedException|UnauthorizedException/i;
function isSuspendedExceptionType(exceptionType, body) {
  const typeMatches = exceptionType != null && SUSPENDED_EXCEPTION_TYPE_RE.test(exceptionType);
  if (!typeMatches)
    return false;
  return body != null && isAccountSuspended(body);
}
function isAccountSuspended(body) {
  if (body.includes("TEMPORARILY_SUSPENDED"))
    return true;
  const json = parseJsonObject(body);
  if (nestedReason(json) === "TEMPORARILY_SUSPENDED")
    return true;
  if (!SUSPENDED_PROSE_RE.test(body))
    return false;
  const type = json?.__type;
  if (type != null && SUSPENDED_EXCEPTION_TYPE_RE.test(String(type)))
    return true;
  return json != null && typeof json.message === "string" && SUSPENDED_PROSE_RE.test(json.message);
}
function isSuspiciousThrottle(body) {
  const lower = body.toLowerCase();
  return lower.includes("throttlingexception") && lower.includes("suspicious activity");
}

// server/lib/kiro/api-client.ts
var BASE_DELAY_MS = 200;
var MAX_DELAY_MS = 2000;
var MAX_THROTTLE_RETRIES = 3;

class KiroApiClient {
  tokenManager;
  constructor(tokenManager) {
    this.tokenManager = tokenManager;
  }
  async sendChatRequest(request, signal, carriedFailedCredentialIds, replaysLeft) {
    const model = request.conversationState.currentMessage.userInputMessage.modelId;
    const maxRetries = this.tokenManager.maxRetries;
    const failedCredentialIds = carriedFailedCredentialIds ?? new Set;
    const remainingReplays = replaysLeft ?? maxRetries;
    let lastError = null;
    const invocationId = randomUUID2();
    const maxAttempts = maxRetries * (MAX_THROTTLE_RETRIES + 1);
    let attemptNumber = 0;
    for (let attempt = 0;attempt < maxRetries; attempt++) {
      let ctx;
      try {
        ctx = await this.tokenManager.acquireContext(model, failedCredentialIds);
      } catch (err) {
        lastError = err instanceof Error ? err : new Error(String(err));
        break;
      }
      const agentMode = request.conversationState.currentMessage.userInputMessage.agentMode ?? request.conversationState.agentTaskType ?? "vibe";
      const headers = this.buildHeaders(ctx, agentMode, invocationId);
      const body = { ...request };
      if (!body.profileArn && ctx.credentials.profileArn) {
        body.profileArn = ctx.credentials.profileArn;
      }
      const region = effectiveApiRegion(this.tokenManager.currentConfig, ctx.credentials);
      const url = `https://q.${region}.amazonaws.com`;
      const tier = classifyKiroCredentialTier(ctx.credentials);
      const slotState = { held: false };
      let res;
      try {
        await acquireSlot(ctx.id, tier, ctx.credentials.subscriptionTitle);
        slotState.held = true;
        res = await this.fetchWithThrottleRetry(() => {
          attemptNumber++;
          headers["amz-sdk-request"] = `attempt=${attemptNumber}; max=${maxAttempts}`;
          return pfetch(url, {
            method: "POST",
            headers,
            body: JSON.stringify(body),
            signal
          });
        }, ctx, slotState);
      } catch (err) {
        if (slotState.held)
          releaseSlot(ctx.id);
        lastError = err instanceof Error ? err : new Error(String(err));
        if (attempt + 1 < maxRetries)
          await this.delay(attempt);
        continue;
      }
      const releaseHeld = () => {
        if (!slotState.held)
          return;
        releaseSlot(ctx.id);
        slotState.held = false;
      };
      if (res.ok) {
        onSuccess(ctx.id);
        this.tokenManager.reportSuccess(ctx.id);
        const failedId = ctx.id;
        return {
          response: res,
          credentialId: failedId,
          release: releaseHeld,
          reportStreamFailure: (reason) => {
            failedCredentialIds.add(failedId);
            return this.tokenManager.reportFailure(failedId, reason);
          },
          retryWithNextCredential: () => {
            if (remainingReplays <= 0) {
              throw new KiroApiError("STREAM_REPLAY_EXHAUSTED", "Exhausted credential replays after mid-stream failures");
            }
            failedCredentialIds.add(failedId);
            return this.sendChatRequest(request, signal, failedCredentialIds, remainingReplays - 1);
          }
        };
      }
      const resBody = await res.text().catch(() => "");
      if (isSuspiciousThrottle(resBody)) {
        onThrottle(ctx.id);
        touchLastRequest(ctx.id);
        recordThrottle(ctx.id);
        releaseHeld();
        const hasFailoverCredential = this.tokenManager.hasAlternativeAvailableCredential(ctx.id, failedCredentialIds);
        if (hasFailoverCredential)
          failedCredentialIds.add(ctx.id);
        this.tokenManager.reportSuspiciousThrottle(ctx.id, failedCredentialIds);
        lastError = new KiroApiError("SUSPICIOUS_THROTTLE", resBody, res.status);
        continue;
      }
      if (res.status === 400) {
        if (isMonthlyRequestLimit(resBody)) {
          releaseHeld();
          failedCredentialIds.add(ctx.id);
          const hasAvailable = this.tokenManager.reportQuotaExhausted(ctx.id);
          if (!hasAvailable)
            throw new KiroQuotaError(resBody);
          lastError = new KiroApiError("QUOTA_EXHAUSTED", resBody, 400);
          continue;
        }
        if (isAccountSuspended(resBody)) {
          releaseHeld();
          failedCredentialIds.add(ctx.id);
          const hasAvailable = this.tokenManager.reportFailure(ctx.id, "account_suspended");
          if (!hasAvailable)
            throw new KiroApiError("ACCOUNT_SUSPENDED", resBody, 400);
          lastError = new KiroApiError("ACCOUNT_SUSPENDED", resBody, 400);
          continue;
        }
        releaseHeld();
        if (isContextLengthExceeded(resBody)) {
          throw new KiroApiError("CONTEXT_LENGTH_EXCEEDED", resBody, 400);
        }
        throw new KiroApiError("BAD_REQUEST", resBody, 400);
      }
      if (res.status === 402 && isMonthlyRequestLimit(resBody)) {
        releaseHeld();
        failedCredentialIds.add(ctx.id);
        const hasAvailable = this.tokenManager.reportQuotaExhausted(ctx.id);
        if (!hasAvailable)
          throw new KiroQuotaError(resBody);
        lastError = new KiroApiError("QUOTA_EXHAUSTED", resBody, 402);
        continue;
      }
      if (res.status === 401 || res.status === 403) {
        releaseHeld();
        failedCredentialIds.add(ctx.id);
        const suspended = isAccountSuspended(resBody);
        const hasAvailable = this.tokenManager.reportFailure(ctx.id, suspended ? "account_suspended" : undefined);
        const code = suspended ? "ACCOUNT_SUSPENDED" : "AUTH_FAILED";
        if (!hasAvailable) {
          throw new KiroApiError(code, resBody, res.status);
        }
        lastError = new KiroApiError(code, resBody, res.status);
        continue;
      }
      if (res.status === 429) {
        releaseHeld();
        throw new KiroApiError("THROTTLED", resBody, 429);
      }
      if (res.status === 408 || res.status >= 500) {
        releaseHeld();
        lastError = new KiroApiError("TRANSIENT", resBody, res.status);
        if (attempt + 1 < maxRetries)
          await this.delay(attempt);
        continue;
      }
      releaseHeld();
      throw new KiroApiError("API_ERROR", resBody, res.status);
    }
    throw lastError ?? new Error("Max retries exceeded");
  }
  async fetchWithThrottleRetry(fetchFn, ctx, slotState) {
    const tier = classifyKiroCredentialTier(ctx.credentials);
    for (let i = 0;; i++) {
      recordRequest(ctx.id);
      const response = await fetchFn();
      if (response.status !== 429 || i >= MAX_THROTTLE_RETRIES)
        return response;
      const bodyText = await response.text().catch(() => "");
      if (isSuspiciousThrottle(bodyText)) {
        return new Response(bodyText, { status: 429, headers: response.headers });
      }
      if (i === 0)
        onThrottle(ctx.id);
      touchLastRequest(ctx.id);
      recordThrottle(ctx.id);
      releaseSlot(ctx.id);
      slotState.held = false;
      await new Promise((resolve3) => setTimeout(resolve3, getBackoffMs(ctx.id)));
      await acquireSlotPriority(ctx.id, tier, ctx.credentials.subscriptionTitle);
      slotState.held = true;
    }
  }
  buildHeaders(ctx, agentMode, invocationId) {
    const config = this.tokenManager.currentConfig;
    const region = effectiveApiRegion(config, ctx.credentials);
    const { kiroVersion } = config;
    const [os, osVersion] = config.systemVersion.split("#");
    const headers = {
      "Content-Type": "application/x-amz-json-1.0",
      "x-amz-target": "AmazonCodeWhispererStreamingService.GenerateAssistantResponse",
      "x-amzn-codewhisperer-optout": "true",
      "x-amzn-kiro-agent-mode": agentMode,
      "User-Agent": `AmazonQ-For-CLI/Version/${kiroVersion}`,
      Host: `q.${region}.amazonaws.com`,
      "amz-sdk-invocation-id": invocationId,
      Authorization: `Bearer ${ctx.token}`,
      "x-amzn-codewhisperer-os": os ?? "linux",
      "x-amzn-codewhisperer-os-version": osVersion ?? "unknown",
      "x-amzn-codewhisperer-ide": "kiro",
      "x-amzn-codewhisperer-ide-version": kiroVersion
    };
    applyKiroTokenTypeHeader(headers, ctx.credentials);
    return headers;
  }
  delay(attempt) {
    const exp = Math.min(BASE_DELAY_MS * 2 ** attempt, MAX_DELAY_MS);
    const jitter = Math.random() * (exp / 4);
    return new Promise((r) => setTimeout(r, exp + jitter));
  }
}

// shared/codewhisperer/runtime.ts
var DEFAULT_CONTINUATION_PROMPT = "Your previous response was interrupted by a temporary connection issue. Please continue from where you left off.";
var silentProtocolLogger = {
  warn: () => {
    return;
  }
};

// shared/codewhisperer/synthetic-id.ts
function generateSyntheticId(size = 21) {
  return nanoid(size);
}

// shared/stream-timeout.ts
var DEFAULT_STALE_TIMEOUT_MS = 5 * 60 * 1000;

class StreamStaleError extends Error {
  constructor(timeoutMs) {
    super(`Stream stale: no data received for ${Math.round(timeoutMs / 1000)}s`);
    this.name = "StreamStaleError";
  }
}
async function readWithTimeout(reader, timeoutMs = DEFAULT_STALE_TIMEOUT_MS) {
  let timer;
  let timedOut = false;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => {
      timedOut = true;
      reject(new StreamStaleError(timeoutMs));
      reader.cancel("Stream stale timeout").catch(() => {});
    }, timeoutMs);
  });
  try {
    const result = await Promise.race([reader.read(), timeout]);
    if (timedOut)
      throw new StreamStaleError(timeoutMs);
    return result;
  } finally {
    if (timer !== undefined)
      clearTimeout(timer);
  }
}

// shared/codewhisperer/error-diagnostics.ts
var ERROR_DIAGNOSTICS_SCHEMA = "narrafork.error-diagnostics.v1";
var MAX_DIAGNOSTIC_TEXT_CHARS2 = 8192;
var MAX_DIAGNOSTIC_HEADERS = 16;
var MAX_DIAGNOSTIC_JSON_CHARS = 16 * 1024;
var DIAGNOSTIC_HEADER_ALLOWLIST = new Set([
  "x-request-id",
  "x-correlation-id",
  "request-id",
  "content-type",
  "retry-after",
  "server"
]);
function isRecord(value) {
  return value != null && typeof value === "object" && !Array.isArray(value);
}
function stringValue(value, max = MAX_DIAGNOSTIC_TEXT_CHARS2) {
  if (typeof value !== "string")
    return;
  const trimmed = value.trim();
  if (!trimmed)
    return;
  return trimmed.length <= max ? trimmed : `${trimmed.slice(0, Math.max(0, max - 32))} [...truncated]`;
}
function numberValue(value) {
  if (typeof value === "number" && Number.isFinite(value))
    return value;
  if (typeof value === "string" && /^\d+$/.test(value.trim())) {
    const parsed = Number(value);
    if (Number.isFinite(parsed))
      return parsed;
  }
  return;
}
function booleanValue(value) {
  return typeof value === "boolean" ? value : undefined;
}
function sanitizeHeaderValue(value) {
  return stringValue(value, 512) ?? "";
}
function normalizeDiagnosticHeaders(value) {
  if (!isRecord(value))
    return;
  const result = {};
  for (const [name, rawValue] of Object.entries(value)) {
    if (Object.keys(result).length >= MAX_DIAGNOSTIC_HEADERS)
      break;
    if (!DIAGNOSTIC_HEADER_ALLOWLIST.has(name.toLowerCase()))
      continue;
    if (typeof rawValue !== "string")
      continue;
    result[name] = sanitizeHeaderValue(rawValue);
  }
  return Object.keys(result).length > 0 ? result : undefined;
}
function diagnosticJsonBytes(result) {
  return Buffer.byteLength(JSON.stringify(result), "utf8");
}
function truncateDiagnosticUtf8(value, maxBytes) {
  const encoded = new TextEncoder().encode(value);
  if (encoded.byteLength <= maxBytes)
    return value;
  return new TextDecoder().decode(encoded.slice(0, Math.max(0, maxBytes)));
}
function limitDiagnosticJsonSize(result) {
  if (diagnosticJsonBytes(result) <= MAX_DIAGNOSTIC_JSON_CHARS)
    return result;
  if (result.responseHeaders)
    delete result.responseHeaders;
  const preferredFields = [
    "responseSnippet",
    "cause",
    "message",
    "source",
    "requestId",
    "providerRequestId",
    "endpoint",
    "channelName",
    "channelType",
    "transport",
    "provider",
    "model",
    "phase",
    "reason",
    "errorType",
    "code"
  ];
  for (const field of preferredFields) {
    let value = result[field];
    if (typeof value !== "string")
      continue;
    while (diagnosticJsonBytes(result) > MAX_DIAGNOSTIC_JSON_CHARS && value.length > 0) {
      const overflow = diagnosticJsonBytes(result) - MAX_DIAGNOSTIC_JSON_CHARS;
      const currentBytes = Buffer.byteLength(value, "utf8");
      const nextBytes = Math.max(0, currentBytes - overflow - 32);
      const next = truncateDiagnosticUtf8(value, nextBytes);
      if (next === value)
        break;
      value = next;
      result[field] = value || undefined;
    }
    if (diagnosticJsonBytes(result) <= MAX_DIAGNOSTIC_JSON_CHARS)
      return result;
  }
  for (const field of preferredFields) {
    if (diagnosticJsonBytes(result) <= MAX_DIAGNOSTIC_JSON_CHARS)
      break;
    if (field !== "source" && field !== "requestId" && field !== "providerRequestId") {
      delete result[field];
    }
  }
  return result;
}
function normalizeApiRequestDiagnostics(input) {
  if (!input)
    return;
  const result = { schema: ERROR_DIAGNOSTICS_SCHEMA };
  const stringFields = [
    "source",
    "phase",
    "reason",
    "errorType",
    "message",
    "responseSnippet",
    "requestId",
    "providerRequestId",
    "provider",
    "model",
    "channelName",
    "channelType",
    "endpoint",
    "transport",
    "cause"
  ];
  for (const field of stringFields) {
    const value = stringValue(input[field]);
    if (value !== undefined)
      result[field] = value;
  }
  if (typeof input.code === "string" || typeof input.code === "number") {
    const value = typeof input.code === "string" ? stringValue(input.code, 512) : input.code;
    if (value !== undefined)
      result.code = value;
  }
  const statusCode = numberValue(input.statusCode);
  if (statusCode != null && statusCode >= 100 && statusCode <= 599)
    result.statusCode = statusCode;
  const retryable = booleanValue(input.retryable);
  if (retryable !== undefined)
    result.retryable = retryable;
  const resumable = booleanValue(input.resumable);
  if (resumable !== undefined)
    result.resumable = resumable;
  const headers = normalizeDiagnosticHeaders(input.responseHeaders);
  if (headers)
    result.responseHeaders = headers;
  return Object.keys(result).length > 1 ? limitDiagnosticJsonSize(result) : undefined;
}
function firstValue(...values) {
  return values.find((value) => value != null && value !== "");
}
function parseErrorDiagnostics(data, defaults = {}) {
  const nested = isRecord(data.error) ? data.error : undefined;
  const supplied = isRecord(data.diagnostics) ? data.diagnostics : undefined;
  const suppliedError = supplied && isRecord(supplied.error) ? supplied.error : undefined;
  const statusCode = firstValue(supplied?.statusCode, supplied?.code, data.statusCode, data.code, nested?.statusCode, nested?.code, defaults.statusCode);
  const code = firstValue(supplied?.code, data.code, nested?.code, suppliedError?.code);
  const reason = firstValue(supplied?.reason, data.reason, nested?.reason, nested?.type, suppliedError?.type, code);
  const message = firstValue(supplied?.message, data.message, nested?.message, suppliedError?.message);
  const responseSnippet = firstValue(supplied?.responseSnippet, data.responseSnippet, nested?.message, suppliedError?.message, message);
  return normalizeApiRequestDiagnostics({
    ...defaults,
    ...supplied,
    statusCode: numberValue(statusCode),
    code: typeof code === "string" || typeof code === "number" ? code : undefined,
    reason: typeof reason === "string" ? reason : undefined,
    errorType: firstValue(supplied?.errorType, nested?.type, suppliedError?.type),
    message,
    responseSnippet,
    requestId: firstValue(supplied?.requestId, data.requestId, data.request_id, nested?.requestId),
    providerRequestId: firstValue(supplied?.providerRequestId, data.providerRequestId),
    retryable: booleanValue(firstValue(supplied?.retryable, data.retryable)),
    resumable: booleanValue(firstValue(supplied?.resumable, data.resumable)),
    responseHeaders: normalizeDiagnosticHeaders(firstValue(supplied?.responseHeaders, data.responseHeaders)),
    cause: firstValue(supplied?.cause, data.cause)
  });
}

// shared/codewhisperer/gateway-events.ts
var QUEUE_EVENT = "queueEvent";
var QUOTA_BALANCE_EVENT = "quotaBalanceEvent";
var MODEL_CATALOG_EVENT = "modelCatalogEvent";
var IMAGE_CACHE_ACK_EVENT = "imageCacheAckEvent";
var METERING_EVENT = "meteringEvent";
var CONTEXT_USAGE_EVENT = "contextUsageEvent";
function isRecord2(value) {
  return value != null && typeof value === "object" && !Array.isArray(value);
}
function formatExtraDetails(value) {
  if (value == null)
    return null;
  if (typeof value === "string")
    return value.trim() || null;
  if (typeof value === "number" || typeof value === "boolean")
    return String(value);
  if (isRecord2(value)) {
    const lines = Object.entries(value).filter(([, entryValue]) => entryValue != null).map(([key, entryValue]) => {
      const formatted = typeof entryValue === "string" || typeof entryValue === "number" || typeof entryValue === "boolean" ? String(entryValue) : JSON.stringify(entryValue);
      return `${key}: ${formatted}`;
    });
    return lines.length > 0 ? lines.join(`
`) : null;
  }
  try {
    return JSON.stringify(value);
  } catch {
    return null;
  }
}
function parseGatewaySSEEvent(eventType, data) {
  if (eventType === QUEUE_EVENT) {
    const position = data.position;
    const queueDepth = data.queueDepth;
    const queueMessage = data.queueMessage;
    if (position != null || queueMessage != null) {
      return {
        queueStatus: {
          position: position != null ? Number(position) : undefined,
          queueDepth: queueDepth != null ? Number(queueDepth) : undefined,
          queueMessage: queueMessage != null ? String(queueMessage) : undefined
        }
      };
    }
    return null;
  }
  if (eventType === QUOTA_BALANCE_EVENT) {
    const balance = data.quotaBalance;
    const detailedBalance = data.detailedQuotaBalance;
    return {
      quotaBalance: balance != null ? String(balance) : null,
      detailedQuotaBalance: formatExtraDetails(detailedBalance) ?? formatExtraDetails(data.extra)
    };
  }
  if (eventType === MODEL_CATALOG_EVENT) {
    if (!Array.isArray(data.models)) {
      return null;
    }
    const modelHash = typeof data.modelHash === "string" ? data.modelHash : typeof data.hash === "string" ? data.hash : undefined;
    const models = data.models.filter(isRecord2);
    return { nugModelCatalog: { modelHash, models } };
  }
  if (eventType === IMAGE_CACHE_ACK_EVENT) {
    if (!Array.isArray(data.refs)) {
      return null;
    }
    const refs = data.refs.filter((ref) => typeof ref === "string" && ref !== "");
    return { nugImageCacheAck: { refs } };
  }
  if (eventType === METERING_EVENT) {
    const usage = numberFrom(data.usage);
    if (usage == null)
      return null;
    return {
      metering: {
        unit: typeof data.unit === "string" ? data.unit : "credit",
        unitPlural: typeof data.unitPlural === "string" ? data.unitPlural : "credits",
        usage
      }
    };
  }
  if (eventType === CONTEXT_USAGE_EVENT) {
    const percentage = numberFrom(data.contextUsagePercentage) ?? numberFrom(data.context_usage_percentage);
    if (percentage == null)
      return null;
    return { contextUsagePercentage: percentage };
  }
  return null;
}
function numberFrom(value) {
  if (typeof value === "number")
    return Number.isFinite(value) ? value : undefined;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : undefined;
  }
  return;
}

// shared/codewhisperer/reasoning-source.ts
function signatureSourcesCompatible(stored, current) {
  if (!stored || !current)
    return false;
  return stored === current;
}

// shared/model-id.ts
function parseModelId(raw) {
  if (!raw)
    return { model: "" };
  const idx = raw.indexOf(":");
  if (idx > 0) {
    const prefix = raw.slice(0, idx);
    return { provider: prefix, model: raw.slice(idx + 1) };
  }
  return { model: raw };
}

// shared/codewhisperer/resolve-model.ts
function resolveModel(model) {
  if (!model)
    return "claude-sonnet-4.5";
  const { model: bare } = parseModelId(model);
  if (!bare)
    return "claude-sonnet-4.5";
  return bare;
}

// shared/codewhisperer/protocol.ts
function buildKiroReasoningContent(reasoningBlocks, redactedThinkingBlocks, currentSource) {
  const texts = [];
  let signature;
  for (const rb of reasoningBlocks ?? []) {
    if (rb.text)
      texts.push(rb.text);
    const sig = rb.providerMetadata?.anthropic?.signature;
    if (sig && signatureSourcesCompatible(rb.providerMetadata?.signatureSource, currentSource)) {
      signature = sig;
    }
  }
  const redacted = redactedThinkingBlocks?.find((rb) => rb.data && signatureSourcesCompatible(rb.signatureSource, currentSource))?.data;
  const hasText = texts.length > 0;
  if (!hasText && !redacted)
    return;
  const reasoningContent = {};
  if (hasText) {
    reasoningContent.reasoningText = {
      text: texts.join(`
`),
      ...signature ? { signature } : {}
    };
  }
  if (redacted) {
    reasoningContent.redactedContent = redacted;
  }
  return reasoningContent;
}
function buildKiroRequest(params) {
  const modelId = resolveModel(params.model);
  const history = params.history;
  const toolResults = params.toolResults;
  const allImages = [];
  if (params.images?.length) {
    for (const img of params.images) {
      allImages.push({
        format: img.format,
        source: { bytes: img.base64 }
      });
    }
  }
  for (const tr of toolResults) {
    const tagged = tr;
    if (tagged._images?.length) {
      allImages.push(...tagged._images);
      delete tagged._images;
      delete tagged._imageLabel;
    }
  }
  return {
    conversationState: {
      conversationId: params.conversationId,
      chatTriggerType: "MANUAL",
      agentTaskType: "vibe",
      agentContinuationId: params.conversationId,
      history: history.length > 0 ? history : undefined,
      currentMessage: {
        userInputMessage: {
          content: params.content,
          modelId,
          origin: "AI_EDITOR",
          ...allImages.length > 0 ? { images: allImages } : {},
          userInputMessageContext: {
            tools: params.tools,
            ...toolResults.length > 0 ? { toolResults } : {}
          }
        }
      },
      envState: {
        operatingSystem: process.platform === "win32" ? "WINDOWS" : process.platform === "darwin" ? "MAC" : "LINUX",
        currentWorkingDirectory: params.cwd
      },
      shellState: {
        shellName: process.env.SHELL?.split("/").pop() ?? "bash"
      }
    }
  };
}
function isKiroUserMessage(message) {
  return "userInputMessage" in message;
}
function isKiroAssistantMessage(message) {
  return "assistantResponseMessage" in message;
}
function cloneToolResult(result) {
  return {
    ...result,
    content: result.content.map((block) => ({ ...block }))
  };
}
function cloneUserMessage(message) {
  const user = message.userInputMessage;
  const ctx = user.userInputMessageContext;
  return {
    userInputMessage: {
      ...user,
      ...user.images ? { images: user.images.map((image) => ({ ...image })) } : {},
      userInputMessageContext: ctx ? {
        ...ctx,
        ...ctx.tools ? { tools: ctx.tools.map((tool) => ({ ...tool })) } : {},
        ...ctx.toolResults ? { toolResults: ctx.toolResults.map((result) => cloneToolResult(result)) } : {}
      } : {}
    }
  };
}
function cloneAssistantMessage(message) {
  const assistant = message.assistantResponseMessage;
  return {
    assistantResponseMessage: {
      ...assistant,
      ...assistant.toolUses ? {
        toolUses: assistant.toolUses.map((toolUse) => ({
          ...toolUse,
          input: { ...toolUse.input }
        }))
      } : {}
    }
  };
}
function isEmptyAssistant(message) {
  const assistant = message.assistantResponseMessage;
  return !assistant.content?.trim() && (assistant.toolUses?.length ?? 0) === 0;
}
function hasUserPayload(message) {
  const user = message.userInputMessage;
  const ctx = user.userInputMessageContext;
  return Boolean(user.content.trim() || (user.images?.length ?? 0) > 0 || (ctx?.tools?.length ?? 0) > 0 || (ctx?.toolResults?.length ?? 0) > 0);
}
function createSyntheticUser(modelId) {
  return {
    userInputMessage: {
      content: "",
      modelId,
      origin: "AI_EDITOR",
      userInputMessageContext: {}
    }
  };
}
function createSyntheticAssistantAck() {
  return { assistantResponseMessage: { content: "OK" } };
}
function mergeUserMessages(target, source) {
  const targetUser = target.userInputMessage;
  const sourceUser = source.userInputMessage;
  const sourceContent = sourceUser.content ?? "";
  if (sourceContent) {
    targetUser.content = targetUser.content ? `${targetUser.content}
${sourceContent}` : sourceContent;
  }
  if (sourceUser.images?.length) {
    targetUser.images = [...targetUser.images ?? [], ...sourceUser.images];
  }
  const targetCtx = targetUser.userInputMessageContext ?? {};
  const sourceCtx = sourceUser.userInputMessageContext ?? {};
  const mergedToolResults = [...targetCtx.toolResults ?? [], ...sourceCtx.toolResults ?? []];
  targetUser.userInputMessageContext = {
    ...targetCtx,
    ...sourceCtx,
    ...sourceCtx.tools ? { tools: sourceCtx.tools } : {},
    ...mergedToolResults.length > 0 ? { toolResults: mergedToolResults } : {}
  };
}
function mergeAssistantMessages(target, source) {
  const targetAssistant = target.assistantResponseMessage;
  const sourceAssistant = source.assistantResponseMessage;
  const sourceContent = sourceAssistant.content ?? "";
  if (sourceContent) {
    targetAssistant.content = targetAssistant.content ? `${targetAssistant.content}
${sourceContent}` : sourceContent;
  }
  if (sourceAssistant.toolUses?.length) {
    targetAssistant.toolUses = [...targetAssistant.toolUses ?? [], ...sourceAssistant.toolUses];
  }
}
function currentMessageCarriesTurn(message) {
  const user = message.userInputMessage;
  const ctx = user.userInputMessageContext;
  return Boolean(user.content.trim() || (user.images?.length ?? 0) > 0 || (ctx?.toolResults?.length ?? 0) > 0);
}
function ensureCurrentMessageCarriesTurn(message, continuationPrompt) {
  if (currentMessageCarriesTurn(message))
    return false;
  message.userInputMessage.content = continuationPrompt;
  return true;
}
function cleanupUserContext(message) {
  const user = message.userInputMessage;
  const ctx = user.userInputMessageContext;
  if (!ctx) {
    user.userInputMessageContext = {};
    return;
  }
  if (ctx.toolResults?.length === 0)
    delete ctx.toolResults;
  user.userInputMessageContext = ctx;
}
function compactKiroMessages(messages, modelId) {
  const compacted = [];
  for (const entry of messages) {
    const msg = entry.message;
    if (isKiroAssistantMessage(msg) && isEmptyAssistant(msg))
      continue;
    if (isKiroUserMessage(msg) && !entry.isCurrent && !hasUserPayload(msg))
      continue;
    const prev = compacted[compacted.length - 1];
    if (isKiroUserMessage(msg)) {
      cleanupUserContext(msg);
      if (prev && isKiroUserMessage(prev.message)) {
        if (entry.isCurrent) {
          compacted.push({ message: createSyntheticAssistantAck(), isCurrent: false });
          compacted.push(entry);
        } else {
          mergeUserMessages(prev.message, msg);
          prev.isCurrent = prev.isCurrent || entry.isCurrent;
        }
        continue;
      }
      compacted.push(entry);
      continue;
    }
    if (prev && isKiroAssistantMessage(prev.message)) {
      mergeAssistantMessages(prev.message, msg);
      continue;
    }
    if (!prev) {
      compacted.push({ message: createSyntheticUser(modelId), isCurrent: false });
    }
    compacted.push(entry);
  }
  return compacted;
}
function sanitizeKiroToolPairs(messages) {
  const consumedToolUseIds = new Set;
  const keep = new Array(messages.length).fill(true);
  for (let i = 0;i < messages.length; i++) {
    const current = messages[i].message;
    if (!isKiroUserMessage(current))
      continue;
    const user = current.userInputMessage;
    const ctx = user.userInputMessageContext ?? {};
    const incomingResults = ctx.toolResults ?? [];
    const prev = i > 0 ? messages[i - 1].message : undefined;
    const prevAssistant = prev && isKiroAssistantMessage(prev) ? prev : undefined;
    const expectedToolUses = prevAssistant?.assistantResponseMessage.toolUses ?? [];
    const expectedIds = new Set(expectedToolUses.map((toolUse) => toolUse.toolUseId).filter((toolUseId) => !consumedToolUseIds.has(toolUseId)));
    const keptResults = [];
    const matchedIds = new Set;
    for (const result of incomingResults) {
      if (!expectedIds.has(result.toolUseId) || matchedIds.has(result.toolUseId))
        continue;
      keptResults.push(result);
      matchedIds.add(result.toolUseId);
      consumedToolUseIds.add(result.toolUseId);
    }
    if (keptResults.length > 0) {
      ctx.toolResults = keptResults;
    } else {
      delete ctx.toolResults;
    }
    user.userInputMessageContext = ctx;
    if (prevAssistant && expectedToolUses.length > 0) {
      const filteredToolUses = expectedToolUses.filter((toolUse) => matchedIds.has(toolUse.toolUseId));
      if (filteredToolUses.length > 0) {
        prevAssistant.assistantResponseMessage.toolUses = filteredToolUses;
      } else {
        delete prevAssistant.assistantResponseMessage.toolUses;
      }
      if (isEmptyAssistant(prevAssistant))
        keep[i - 1] = false;
    }
    if (!messages[i].isCurrent && !hasUserPayload(current))
      keep[i] = false;
  }
  return messages.filter((_, index) => keep[index]);
}
function normalizeKiroChatRequest(request, runtime = {}) {
  const log2 = runtime.logger ?? silentProtocolLogger;
  const continuationPrompt = runtime.continuationPrompt ?? DEFAULT_CONTINUATION_PROMPT;
  const cs = request.conversationState;
  const currentMessage = cloneUserMessage(cs.currentMessage);
  const modelId = currentMessage.userInputMessage.modelId;
  const initialMessages = [
    ...(cs.history ?? []).map((message) => ({
      message: isKiroUserMessage(message) ? cloneUserMessage(message) : cloneAssistantMessage(message),
      isCurrent: false
    })),
    { message: currentMessage, isCurrent: true }
  ];
  let normalized = compactKiroMessages(initialMessages, modelId);
  normalized = sanitizeKiroToolPairs(normalized);
  normalized = compactKiroMessages(normalized, modelId);
  normalized = sanitizeKiroToolPairs(normalized);
  normalized = compactKiroMessages(normalized, modelId);
  const currentIndex = normalized.findIndex((entry) => entry.isCurrent);
  const effectiveCurrentIndex = currentIndex >= 0 ? currentIndex : normalized.length - 1;
  const beforeCurrent = normalized.slice(0, effectiveCurrentIndex);
  const current = normalized[effectiveCurrentIndex]?.message;
  if (!current || !isKiroUserMessage(current)) {
    const fallbackCurrent = currentMessage;
    const injected2 = ensureCurrentMessageCarriesTurn(fallbackCurrent, continuationPrompt);
    if (injected2) {
      log2.warn("Kiro request had an empty current message; injected a continuation prompt", {
        model: modelId,
        route: "current_turn_not_user",
        historyLength: beforeCurrent.length
      });
    }
    cs.history = beforeCurrent.map((entry) => entry.message);
    cs.currentMessage = { userInputMessage: fallbackCurrent.userInputMessage };
    return request;
  }
  const injected = ensureCurrentMessageCarriesTurn(current, continuationPrompt);
  if (injected) {
    log2.warn("Kiro request had an empty current message; injected a continuation prompt", {
      model: modelId,
      route: "empty_after_normalize",
      historyLength: beforeCurrent.length
    });
  }
  cs.history = beforeCurrent.length > 0 ? beforeCurrent.map((entry) => entry.message) : undefined;
  cs.currentMessage = { userInputMessage: current.userInputMessage };
  return request;
}
function isRecord3(value) {
  return typeof value === "object" && value !== null;
}
function numberFrom2(value) {
  if (typeof value === "number" && Number.isFinite(value))
    return value;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed))
      return parsed;
  }
  return;
}
function firstNumber(source, keys) {
  for (const key of keys) {
    const value = numberFrom2(source[key]);
    if (value != null)
      return value;
  }
  return;
}
function parseUsagePayload(data) {
  const source = isRecord3(data.usage) ? data.usage : data;
  const inputTokens = firstNumber(source, [
    "inputTokens",
    "input_tokens",
    "promptTokens",
    "prompt_tokens"
  ]);
  const completionTokens = firstNumber(source, [
    "completionTokens",
    "completion_tokens",
    "outputTokens",
    "output_tokens"
  ]);
  const cachedInputTokens = firstNumber(source, [
    "cachedInputTokens",
    "cached_input_tokens",
    "cacheReadInputTokens",
    "cache_read_input_tokens"
  ]);
  const cacheCreationInputTokens = firstNumber(source, [
    "cacheCreationInputTokens",
    "cache_creation_input_tokens",
    "cacheWriteInputTokens",
    "cache_write_input_tokens"
  ]);
  const cacheCreation5mTokens = firstNumber(source, [
    "cacheCreation5mTokens",
    "cache_creation_5m_tokens",
    "ephemeral5mInputTokens",
    "ephemeral_5m_input_tokens"
  ]);
  const cacheCreation1hTokens = firstNumber(source, [
    "cacheCreation1hTokens",
    "cache_creation_1h_tokens",
    "ephemeral1hInputTokens",
    "ephemeral_1h_input_tokens"
  ]);
  const reasoningTokens = firstNumber(source, ["reasoningTokens", "reasoning_tokens"]);
  const hasAny = [
    inputTokens,
    completionTokens,
    cachedInputTokens,
    cacheCreationInputTokens,
    cacheCreation5mTokens,
    cacheCreation1hTokens,
    reasoningTokens
  ].some((value) => value != null);
  if (!hasAny)
    return;
  const derivedCacheCreation = cacheCreationInputTokens ?? (cacheCreation5mTokens ?? 0) + (cacheCreation1hTokens ?? 0);
  const promptTokens = firstNumber(source, ["promptTokens", "prompt_tokens"]) ?? (inputTokens ?? 0) + (cachedInputTokens ?? 0) + derivedCacheCreation;
  return {
    promptTokens,
    inputTokens,
    completionTokens,
    reasoningTokens,
    cachedInputTokens,
    cacheCreationInputTokens: derivedCacheCreation,
    cacheCreation5mTokens,
    cacheCreation1hTokens
  };
}
function readXmlAttribute(attrs, name) {
  const match = attrs.match(new RegExp(`\\b${name}\\s*=\\s*(["'])([\\s\\S]*?)\\1`, "i"));
  return match ? decodeXmlEntities(match[2]) : undefined;
}
function decodeXmlEntities(value) {
  return value.replace(/&(#x?[0-9a-f]+|[a-z]+);/gi, (entity, body) => {
    const lower = body.toLowerCase();
    if (lower === "amp")
      return "&";
    if (lower === "lt")
      return "<";
    if (lower === "gt")
      return ">";
    if (lower === "quot")
      return '"';
    if (lower === "apos")
      return "'";
    if (lower.startsWith("#x")) {
      const code = Number.parseInt(lower.slice(2), 16);
      return Number.isInteger(code) && code >= 0 && code <= 1114111 ? String.fromCodePoint(code) : entity;
    }
    if (lower.startsWith("#")) {
      const code = Number.parseInt(lower.slice(1), 10);
      return Number.isInteger(code) && code >= 0 && code <= 1114111 ? String.fromCodePoint(code) : entity;
    }
    return entity;
  });
}
var MAX_MALFORMED_INVOKE_MARKER_LENGTH = 10;
var MALFORMED_INVOKE_MARKERS = new Set(["call", "count"]);
var XML_BOOLEAN_PARAMETER_NAMES = new Set([
  "replace_all",
  "run_in_background",
  "strict_serial",
  "compress",
  "preview",
  "await",
  "doInterrupt",
  "multiSelect",
  "confirm"
]);
var XML_NUMBER_PARAMETER_NAMES = new Set([
  "timeout",
  "limit",
  "offset",
  "head_limit",
  "maxChars",
  "max_length",
  "pageno",
  "duration",
  "count"
]);
var XML_JSON_PARAMETER_NAMES = new Set([
  "ids",
  "names",
  "paths",
  "todos",
  "questions",
  "answers",
  "annotations",
  "metadata",
  "headers",
  "options",
  "tool_uses"
]);
function stripMalformedInvokeMarker(text) {
  return text.replace(/(\r?\n[ \t]*\r?\n[ \t]*)([^\r\n]*?)(?:\r?\n[ \t]*)*$/u, (match, blankSeparator, marker) => {
    const trimmed = marker.trim();
    const normalized = trimmed.toLowerCase();
    const isKnownMarker = MALFORMED_INVOKE_MARKERS.has(normalized);
    if (trimmed.length === 0 || trimmed.length > MAX_MALFORMED_INVOKE_MARKER_LENGTH && !isKnownMarker) {
      return match;
    }
    return blankSeparator;
  });
}
function coerceKiroXmlParameter(paramName, value) {
  const trimmed = value.trim();
  if (XML_BOOLEAN_PARAMETER_NAMES.has(paramName)) {
    if (/^(true|false)$/i.test(trimmed))
      return trimmed.toLowerCase() === "true";
  }
  if (XML_NUMBER_PARAMETER_NAMES.has(paramName)) {
    if (/^-?(?:\d+|\d*\.\d+)$/.test(trimmed)) {
      const parsed = Number(trimmed);
      if (Number.isFinite(parsed))
        return parsed;
    }
  }
  if (XML_JSON_PARAMETER_NAMES.has(paramName) && (trimmed.startsWith("[") || trimmed.startsWith("{"))) {
    try {
      return JSON.parse(trimmed);
    } catch {}
  }
  return value;
}
var PARAMETER_OPEN_PATTERN = /<parameter\b([^>]*)>/gi;
var PARAMETER_CLOSE_PATTERN = /<\/parameter\s*>/i;
function normalizeUnterminatedParameterValue(value) {
  return value.replace(/\s+$/u, "").replace(/"?>$/u, "").replace(/\s+$/u, "");
}
function parseInvokeParameters(body) {
  const input = {};
  const openRe = new RegExp(PARAMETER_OPEN_PATTERN.source, PARAMETER_OPEN_PATTERN.flags);
  let openMatch = openRe.exec(body);
  while (openMatch) {
    const valueStart = openMatch.index + openMatch[0].length;
    const nextOpenMatch = openRe.exec(body);
    const nextOpenIndex = nextOpenMatch?.index ?? body.length;
    const rest = body.slice(valueStart, nextOpenIndex);
    const closeMatch = PARAMETER_CLOSE_PATTERN.exec(rest);
    const rawValue = closeMatch ? rest.slice(0, closeMatch.index) : rest;
    const value = closeMatch ? rawValue : normalizeUnterminatedParameterValue(rawValue);
    const paramName = readXmlAttribute(openMatch[1] ?? "", "name");
    if (paramName) {
      input[paramName] = coerceKiroXmlParameter(paramName, decodeXmlEntities(value));
    }
    openMatch = nextOpenMatch;
  }
  return input;
}
function buildInvokeToolUse(attrs, body) {
  const name = readXmlAttribute(attrs, "name");
  if (!name)
    return null;
  return {
    toolUseId: `kiro_xml_${generateSyntheticId(12)}`,
    name,
    input: parseInvokeParameters(body)
  };
}
function parseKiroTextToolCalls(text) {
  const invokeRe = /<invoke\b([^>]*)>([\s\S]*?)<\/invoke>/gi;
  let cursor = 0;
  let rewrittenText = "";
  const toolUses = [];
  for (const invokeMatch of text.matchAll(invokeRe)) {
    const matchIndex = invokeMatch.index ?? 0;
    const rawInvoke = invokeMatch[0];
    const toolUse = buildInvokeToolUse(invokeMatch[1] ?? "", invokeMatch[2] ?? "");
    if (!toolUse)
      continue;
    rewrittenText += stripMalformedInvokeMarker(text.slice(cursor, matchIndex));
    toolUses.push(toolUse);
    cursor = matchIndex + rawInvoke.length;
  }
  if (toolUses.length === 0)
    return { text, toolUses };
  rewrittenText += text.slice(cursor);
  return { text: rewrittenText, toolUses };
}
var INVOKE_OPEN_RE = /<invoke\b/i;
var INVOKE_BLOCK_FROM_START_RE = /^<invoke\b([^>]*)>([\s\S]*?)<\/invoke>/i;

class KiroTextToolCallStreamParser {
  buffer = "";
  push(delta) {
    this.buffer += delta;
    return this.drain(false);
  }
  flush() {
    return this.drain(true);
  }
  drain(final) {
    let emittedText = "";
    const toolUses = [];
    while (this.buffer.length > 0) {
      const open = INVOKE_OPEN_RE.exec(this.buffer);
      if (!open) {
        const keep = final ? this.buffer.length : this.retentionStart(this.buffer);
        emittedText += this.buffer.slice(0, keep);
        this.buffer = this.buffer.slice(keep);
        break;
      }
      const openIdx = open.index;
      const block = INVOKE_BLOCK_FROM_START_RE.exec(this.buffer.slice(openIdx));
      if (block) {
        emittedText += stripMalformedInvokeMarker(this.buffer.slice(0, openIdx));
        const toolUse = buildInvokeToolUse(block[1] ?? "", block[2] ?? "");
        if (toolUse) {
          toolUses.push(toolUse);
        } else {
          emittedText += block[0];
        }
        this.buffer = this.buffer.slice(openIdx + block[0].length);
        continue;
      }
      if (final) {
        emittedText += this.buffer;
        this.buffer = "";
        break;
      }
      emittedText += stripMalformedInvokeMarker(this.buffer.slice(0, openIdx));
      this.buffer = this.buffer.slice(openIdx);
      break;
    }
    return { text: emittedText, toolUses };
  }
  retentionStart(buf) {
    let start = buf.length;
    const target = "<invoke";
    const max = Math.min(target.length - 1, buf.length);
    for (let len = max;len >= 1; len--) {
      if (buf.slice(buf.length - len).toLowerCase() === target.slice(0, len).toLowerCase()) {
        start = buf.length - len;
        break;
      }
    }
    const markerTail = /(\r?\n[ \t]*\r?\n[ \t]*)([^\r\n]*)((?:\r?\n[ \t]*)*)$/.exec(buf);
    if (markerTail && markerTail.index < start) {
      const marker = markerTail[2].trim();
      const isKnown = MALFORMED_INVOKE_MARKERS.has(marker.toLowerCase());
      if (marker.length === 0 || marker.length <= MAX_MALFORMED_INVOKE_MARKER_LENGTH || isKnown) {
        start = markerTail.index;
      }
    }
    return start;
  }
}
var HALLUCINATION_LATIN_MARKERS = ["course", "card", "call", "count"];
var HALLUCINATION_CJK_MARKERS = ["\u8BFE"];
var HALLUCINATION_LINE_HEAD_MARKERS = [
  ...HALLUCINATION_LATIN_MARKERS,
  ...HALLUCINATION_CJK_MARKERS
];
var HALLUCINATION_MODEL_PATTERN = /opus-(?:4[.-]8|5(?!\d))/i;
function shouldFilterHallucinatedMarkers(model) {
  return model != null && HALLUCINATION_MODEL_PATTERN.test(model);
}
var escapeMarker = (m) => m.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
var LATIN_MARKER_ALTERNATION = HALLUCINATION_LATIN_MARKERS.map(escapeMarker).join("|");
var CJK_MARKER_ALTERNATION = HALLUCINATION_CJK_MARKERS.map(escapeMarker).join("|");
var HALLUCINATION_LINE_HEAD_RE = new RegExp(`^([ \\t]*)(?:(?:${LATIN_MARKER_ALTERNATION})(?![a-zA-Z0-9])[.,;:!?]?|(?:${CJK_MARKER_ALTERNATION})(?=$|[ \\t\\r\\n]))[ \\t]*`, "u");
var CODE_FENCE_RE = /^[ \t]{0,3}(`{3,}|~{3,})([^\r\n]*)$/;
var PARTIAL_CODE_FENCE_RE = /^[ \t]{0,3}(?:(?:`{1,2}|~{1,2})$|(?:`{3,}|~{3,})[^\r\n]*$)/;
var LATIN_MARKER_UNSETTLED_TAIL_RE = new RegExp(`^(?:${LATIN_MARKER_ALTERNATION})[.,;:!?]?[ \\t]*$`);

class KiroHallucinationMarkerFilter {
  buffer = "";
  atLineStart = false;
  openFence = null;
  push(delta) {
    this.buffer += delta;
    let out = "";
    const lastNewline = this.buffer.lastIndexOf(`
`);
    if (lastNewline >= 0) {
      out += this.consume(this.buffer.slice(0, lastNewline + 1));
      this.buffer = this.buffer.slice(lastNewline + 1);
    }
    if (this.canEmitPartialLine(this.buffer)) {
      out += this.consume(this.buffer);
      this.buffer = "";
    }
    return out;
  }
  flush() {
    const emit = this.consume(this.buffer);
    this.buffer = "";
    return emit;
  }
  consume(text) {
    if (!text)
      return text;
    let out = "";
    let pos = 0;
    while (pos < text.length) {
      const nl = text.indexOf(`
`, pos);
      const lineEnd = nl < 0 ? text.length : nl + 1;
      const raw = text.slice(pos, lineEnd);
      out += this.consumeLine(raw);
      this.atLineStart = nl >= 0;
      pos = lineEnd;
    }
    return out;
  }
  consumeLine(raw) {
    const content = raw.replace(/\r?\n$/, "");
    const fence = CODE_FENCE_RE.exec(content);
    if (this.openFence) {
      if (fence && this.closesFence(fence[1], fence[2]))
        this.openFence = null;
      return raw;
    }
    if (fence) {
      this.openFence = { char: fence[1][0], length: fence[1].length };
      return raw;
    }
    if (!this.atLineStart)
      return raw;
    return raw.replace(HALLUCINATION_LINE_HEAD_RE, (_m, indent) => indent);
  }
  closesFence(run, info) {
    const open = this.openFence;
    if (!open)
      return false;
    return run[0] === open.char && run.length >= open.length && info.trim().length === 0;
  }
  canEmitPartialLine(tail) {
    if (tail.length === 0)
      return true;
    if (tail === "\r")
      return false;
    if (!this.atLineStart)
      return true;
    if (PARTIAL_CODE_FENCE_RE.test(tail))
      return false;
    if (this.openFence)
      return true;
    const head = tail.replace(/^[ \t]*/, "");
    if (couldBeMarkerPrefix(head))
      return false;
    if (LATIN_MARKER_UNSETTLED_TAIL_RE.test(head))
      return false;
    return true;
  }
}
function couldBeMarkerPrefix(text) {
  if (text.length === 0)
    return true;
  for (const marker of HALLUCINATION_LINE_HEAD_MARKERS) {
    if (marker.startsWith(text) && text.length <= marker.length)
      return true;
  }
  return false;
}

class KiroStreamToolCallAccumulator {
  parser;
  markerFilter;
  constructor(options = {}) {
    this.parser = options.parseTextToolCalls ? new KiroTextToolCallStreamParser : null;
    this.markerFilter = shouldFilterHallucinatedMarkers(options.model) ? new KiroHallucinationMarkerFilter : null;
  }
  process(parsed) {
    if (parsed.text != null && this.markerFilter) {
      parsed.text = this.markerFilter.push(parsed.text);
    }
    if (!this.parser || parsed.text == null)
      return parsed;
    const { text, toolUses } = this.parser.push(parsed.text);
    parsed.text = text;
    appendDedupedToolUses(parsed, toolUses);
    return parsed;
  }
  finalize() {
    let markerTail = "";
    if (this.markerFilter)
      markerTail = this.markerFilter.flush();
    if (!this.parser) {
      if (!markerTail)
        return null;
      return { text: markerTail };
    }
    let pushedText = "";
    const pushedToolUses = [];
    if (markerTail) {
      const pushed = this.parser.push(markerTail);
      pushedText = pushed.text;
      pushedToolUses.push(...pushed.toolUses);
    }
    const { text: flushedText, toolUses: flushedToolUses } = this.parser.flush();
    const text = pushedText + flushedText;
    const toolUses = [...pushedToolUses, ...flushedToolUses];
    if (!text && toolUses.length === 0)
      return null;
    const parsed = {};
    if (text)
      parsed.text = text;
    appendDedupedToolUses(parsed, toolUses);
    return parsed;
  }
}
function stableJson(value) {
  if (Array.isArray(value))
    return `[${value.map((item) => stableJson(item)).join(",")}]`;
  if (value && typeof value === "object") {
    const entries = Object.entries(value).sort(([a], [b]) => a.localeCompare(b));
    return `{${entries.map(([key, item]) => `${JSON.stringify(key)}:${stableJson(item)}`).join(",")}}`;
  }
  return JSON.stringify(value);
}
function isDuplicateToolUse(candidate, existing) {
  return candidate.name === existing.name && stableJson(candidate.input) === stableJson(existing.input);
}
function appendDedupedToolUses(result, toolUses) {
  if (!toolUses?.length)
    return;
  const next = [...result.toolUses ?? []];
  for (const toolUse of toolUses) {
    if (!next.some((item) => isDuplicateToolUse(toolUse, item)))
      next.push(toolUse);
  }
  result.toolUses = next;
}
function kiroStopReason(raw) {
  if (typeof raw !== "string" || raw.trim().length === 0)
    return;
  switch (raw.trim().toUpperCase()) {
    case "END_TURN":
    case "STOP_SEQUENCE":
      return "end_turn";
    case "MAX_TOKENS":
    case "MAX_OUTPUT_TOKENS":
    case "LENGTH":
      return "max_output_tokens";
    case "TOOL_USE":
      return "tool_use";
    case "CONTENT_FILTERED":
    case "CONTENT_FILTER":
      return "content_filter";
    default:
      return;
  }
}
function parseKiroStreamEvent(evt, options = {}) {
  const gatewayEvent = parseGatewaySSEEvent(evt.eventType, evt.data);
  if (gatewayEvent)
    return gatewayEvent;
  const result = {};
  const usage = parseUsagePayload(evt.data);
  if (usage)
    result.usage = usage;
  if (evt.eventType === "assistantResponseEvent") {
    const toolUse = evt.data.toolUse;
    const toolUses = evt.data.toolUses;
    let parsedTextToolUses;
    if (evt.data.content != null) {
      const contentText = String(evt.data.content);
      if (options.parseTextToolCalls) {
        const parsedContent = parseKiroTextToolCalls(contentText);
        result.text = parsedContent.text;
        parsedTextToolUses = parsedContent.toolUses;
      } else {
        result.text = contentText;
      }
    }
    if (evt.data.messageId) {
      result.messageId = String(evt.data.messageId);
    }
    if (evt.data.conversationId) {
      result.conversationId = String(evt.data.conversationId);
    }
    const stopReason = evt.data.stopReason ?? evt.data.stop_reason;
    if (stopReason === "max_tokens" || stopReason === "max_output_tokens" || stopReason === "length") {
      result.invalidState = {
        reason: "max_tokens",
        message: "Response truncated: model reached maximum token limit."
      };
    }
    if (toolUse?.toolUseId) {
      appendDedupedToolUses(result, [
        {
          toolUseId: toolUse.toolUseId,
          name: toolUse.name,
          input: toolUse.input ?? {}
        }
      ]);
    }
    if (Array.isArray(toolUses) && toolUses.length > 0) {
      appendDedupedToolUses(result, toolUses.map((tu) => ({
        toolUseId: tu.toolUseId,
        name: tu.name,
        input: tu.input ?? {}
      })));
    }
    appendDedupedToolUses(result, parsedTextToolUses);
  }
  if (evt.eventType === "toolUseEvent") {
    const id = evt.data.toolUseId;
    if (id) {
      result.toolUseChunk = {
        toolUseId: id,
        name: evt.data.name,
        input: typeof evt.data.input === "string" ? evt.data.input : undefined,
        stop: evt.data.stop === true
      };
    }
  }
  if (evt.eventType === "reasoningContentEvent") {
    const content = evt.data.content ?? evt.data.text;
    if (content != null) {
      result.reasoning = String(content);
    }
    const signature = evt.data.signature ?? evt.data.reasoningSignature;
    if (signature != null) {
      result.reasoningMetadata = {
        ...result.reasoningMetadata,
        anthropic: {
          ...result.reasoningMetadata?.anthropic,
          signature: String(signature)
        }
      };
    }
    const redacted = evt.data.redactedContent ?? evt.data.redacted_content;
    if (redacted != null) {
      result.redactedThinking = { data: String(redacted) };
    }
  }
  if (evt.eventType === "contextUsageEvent") {
    const pct = evt.data.contextUsagePercentage ?? evt.data.context_usage_percentage;
    if (pct != null) {
      result.contextUsagePercentage = Number(pct);
    }
  }
  if (evt.eventType === "meteringEvent") {
    const usage2 = evt.data.usage;
    if (usage2 != null) {
      result.metering = {
        unit: String(evt.data.unit ?? "credit"),
        unitPlural: String(evt.data.unitPlural ?? "credits"),
        usage: Number(usage2)
      };
    }
  }
  if (evt.eventType === "metadataEvent") {
    const reason = kiroStopReason(evt.data.stopReason);
    if (reason)
      result.stopReason = reason;
  }
  if (evt.eventType === "credentialIdEvent") {
    const id = evt.data.credentialId;
    if (id != null) {
      result.credentialId = String(id);
    }
  }
  if (evt.eventType === "invalidStateEvent") {
    result.invalidState = {
      reason: String(evt.data.reason ?? "unknown"),
      message: String(evt.data.message ?? evt.data.content ?? "Invalid state"),
      diagnostics: parseErrorDiagnostics(evt.data, {
        source: "provider",
        phase: "invalid_state_event"
      })
    };
  }
  if (evt.eventType === "error") {
    const diagnostics = parseErrorDiagnostics(evt.data, {
      source: "gateway",
      phase: "sse_error"
    });
    result.invalidState = {
      reason: diagnostics?.reason ?? "gateway_error",
      message: diagnostics?.message ?? String(evt.data.message ?? "Gateway proxy error"),
      diagnostics
    };
  }
  return result;
}

// shared/codewhisperer/canonical-request.ts
var REASONING_SOURCE = "kiro";
var SYSTEM_PROMPT_ACK_EN = "I will follow these instructions.";
var SYSTEM_PROMPT_ACK = {
  en: SYSTEM_PROMPT_ACK_EN,
  "zh-CN": "\u6211\u4F1A\u9075\u5FAA\u8FD9\u4E9B\u6307\u793A\u3002"
};
function systemPromptAck(locale) {
  if (!locale)
    return SYSTEM_PROMPT_ACK_EN;
  return SYSTEM_PROMPT_ACK[locale] ?? SYSTEM_PROMPT_ACK_EN;
}
function isRecord4(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
function asString(value) {
  return typeof value === "string" && value.length > 0 ? value : undefined;
}
function blocksOf(message) {
  if (!Array.isArray(message.content))
    return [];
  return message.content.filter((block) => {
    return isRecord4(block) && typeof block.type === "string";
  });
}
function imageFormat(mediaTypeOrFormat) {
  const raw = asString(mediaTypeOrFormat)?.toLowerCase() ?? "";
  const bare = raw.replace(/^image\//, "");
  if (bare === "jpeg" || bare === "jpg")
    return "jpeg";
  if (bare === "gif")
    return "gif";
  if (bare === "webp")
    return "webp";
  return "png";
}
function toKiroImage(block) {
  const data = asString(block.dataBase64) ?? asString(block.base64);
  if (!data)
    return;
  return {
    format: imageFormat(block.mediaType ?? block.format),
    source: { bytes: data }
  };
}
function textOf(blocks) {
  return blocks.filter((block) => block.type === "text").map((block) => asString(block.text) ?? "").filter((text) => text.length > 0).join(`
`);
}
function auxiliaryText(blocks) {
  const parts = [];
  for (const block of blocks) {
    if (block.type === "web_search") {
      const queries = Array.isArray(block.queries) ? block.queries.filter((query2) => typeof query2 === "string") : [];
      const query = asString(block.query) ?? (queries.length > 0 ? queries.join(", ") : "unknown");
      parts.push(`[Web search: ${query}]`);
      continue;
    }
    if (block.type === "image_generation") {
      const revised = asString(block.revisedPrompt);
      parts.push(revised ? `[Image generated: ${revised}]` : "[Image generated]");
    }
  }
  return parts.join(`
`);
}
function reasoningContentOf(blocks) {
  const reasoningBlocks = [];
  const redactedBlocks = [];
  for (const block of blocks) {
    if (block.type === "reasoning") {
      const text = asString(block.text) ?? "";
      const continuation = isRecord4(block.continuation) ? block.continuation : undefined;
      const signature = continuation?.format === "anthropic" && isRecord4(continuation.data) ? asString(continuation.data.anthropic?.signature) : undefined;
      reasoningBlocks.push({
        text,
        ...signature ? {
          providerMetadata: {
            anthropic: { signature },
            signatureSource: REASONING_SOURCE
          }
        } : {}
      });
      continue;
    }
    if (block.type === "redacted_reasoning") {
      const data = asString(block.data);
      if (!data)
        continue;
      redactedBlocks.push({
        data,
        ...asString(block.source) ? { signatureSource: REASONING_SOURCE } : {}
      });
    }
  }
  if (reasoningBlocks.length === 0 && redactedBlocks.length === 0)
    return;
  return buildKiroReasoningContent(reasoningBlocks, redactedBlocks, REASONING_SOURCE);
}
function toKiroToolResult(block) {
  const toolUseId = asString(block.toolUseId) ?? asString(block.tool_call_id);
  if (!toolUseId)
    return;
  const parts = [];
  const images = [];
  const content = Array.isArray(block.content) ? block.content : [];
  for (const entry of content) {
    if (!isRecord4(entry))
      continue;
    if (entry.type === "text") {
      const text = asString(entry.text);
      if (text)
        parts.push(text);
      continue;
    }
    if (entry.type === "image") {
      const image = toKiroImage(entry);
      if (image)
        images.push(image);
    }
  }
  const isError = block.isError === true || block.is_error === true;
  return {
    result: {
      toolUseId,
      content: [{ text: parts.join(`
`) }],
      status: isError ? "error" : "success",
      isError
    },
    images
  };
}
function toKiroTools(tools) {
  if (!Array.isArray(tools))
    return [];
  const result = [];
  for (const tool of tools) {
    if (!isRecord4(tool))
      continue;
    const name = asString(tool.name);
    if (!name)
      continue;
    const schema = isRecord4(tool.inputSchema) ? tool.inputSchema : isRecord4(tool.parameters) ? tool.parameters : {};
    result.push({
      toolSpecification: {
        name,
        description: typeof tool.description === "string" ? tool.description : "",
        inputSchema: { json: ensureRequiredProperty(schema) }
      }
    });
  }
  return result;
}
function ensureRequiredProperty(schema) {
  const required = Array.isArray(schema.required) ? schema.required : undefined;
  if (required && required.length > 0)
    return schema;
  const properties = isRecord4(schema.properties) ? schema.properties : undefined;
  return {
    ...schema,
    properties: {
      ...properties ?? {},
      confirm: {
        type: "boolean",
        description: "Dummy parameter (always pass true)",
        const: true,
        default: true
      }
    },
    required: ["confirm"]
  };
}
function emptyPendingUser() {
  return { text: [], images: [], toolResults: [] };
}
function pendingUserHasContent(pending) {
  return pending.text.some((text) => text.trim().length > 0) || pending.images.length > 0 || pending.toolResults.length > 0;
}
function commitPendingUser(history, pending, modelId) {
  if (!pendingUserHasContent(pending))
    return;
  const context = pending.toolResults.length > 0 ? { toolResults: pending.toolResults } : {};
  const message = {
    userInputMessage: {
      content: pending.text.join(`
`),
      modelId,
      origin: "AI_EDITOR",
      ...pending.images.length > 0 ? { images: pending.images } : {},
      userInputMessageContext: context
    }
  };
  history.push(message);
}
function toKiroHistory(messages, modelId, locale, ack) {
  const history = [];
  if (!Array.isArray(messages))
    return { history };
  const systemAck = ack ?? systemPromptAck(locale);
  let pending = emptyPendingUser();
  for (const raw of messages) {
    if (!isRecord4(raw) || typeof raw.role !== "string")
      continue;
    const message = { ...raw, role: raw.role };
    const blocks = blocksOf(message);
    if (message.role === "system") {
      commitPendingUser(history, pending, modelId);
      pending = emptyPendingUser();
      const text = textOf(blocks);
      if (!text)
        continue;
      history.push({
        userInputMessage: { content: text, modelId, userInputMessageContext: {} }
      });
      history.push({ assistantResponseMessage: { content: systemAck } });
      continue;
    }
    if (message.role === "tool") {
      for (const block of blocks) {
        if (block.type !== "tool_result")
          continue;
        const converted = toKiroToolResult(block);
        if (!converted)
          continue;
        pending.toolResults.push(converted.result);
        for (const _image of converted.images) {
          pending.text.push("[Image omitted from tool result]");
        }
      }
      continue;
    }
    if (message.role === "user") {
      const text = textOf(blocks);
      if (text)
        pending.text.push(text);
      for (const block of blocks) {
        if (block.type === "image") {
          const image = toKiroImage(block);
          if (image)
            pending.images.push(image);
          continue;
        }
        if (block.type === "tool_result") {
          const converted = toKiroToolResult(block);
          if (!converted)
            continue;
          pending.toolResults.push(converted.result);
          for (const image of converted.images)
            pending.images.push(image);
        }
      }
      commitPendingUser(history, pending, modelId);
      pending = emptyPendingUser();
      continue;
    }
    if (message.role !== "assistant")
      continue;
    commitPendingUser(history, pending, modelId);
    pending = emptyPendingUser();
    const texts = [];
    const primary = textOf(blocks);
    if (primary)
      texts.push(primary);
    const auxiliary = auxiliaryText(blocks);
    if (auxiliary)
      texts.push(auxiliary);
    const toolUses = [];
    for (const block of blocks) {
      if (block.type !== "tool_call")
        continue;
      const toolUseId = asString(block.toolUseId);
      const name = asString(block.name);
      if (!toolUseId || !name)
        continue;
      toolUses.push({
        toolUseId,
        name,
        input: isRecord4(block.input) ? block.input : {}
      });
    }
    const reasoningContent = reasoningContentOf(blocks);
    history.push({
      assistantResponseMessage: {
        content: texts.join(`
`),
        ...reasoningContent ? { reasoning_content: reasoningContent } : {},
        ...toolUses.length > 0 ? { toolUses } : {}
      }
    });
  }
  commitPendingUser(history, pending, modelId);
  const last = history[history.length - 1];
  if (last && "userInputMessage" in last) {
    history.push({ assistantResponseMessage: { content: "OK" } });
  }
  return { history };
}
function toKiroRequestParts(input) {
  const { history } = toKiroHistory(input.history, input.modelId, input.locale, input.ack);
  const tools = toKiroTools(input.tools);
  const current = input.current ?? {};
  const toolResults = [];
  const images = [];
  if (Array.isArray(current.images)) {
    for (const entry of current.images) {
      if (!isRecord4(entry))
        continue;
      const image = toKiroImage(entry);
      if (image)
        images.push(image);
    }
  }
  if (Array.isArray(current.toolResults)) {
    for (const entry of current.toolResults) {
      if (!isRecord4(entry) || typeof entry.type !== "string")
        continue;
      const converted = toKiroToolResult(entry);
      if (!converted)
        continue;
      toolResults.push(converted.result);
      for (const image of converted.images)
        images.push(image);
    }
  }
  return {
    history,
    tools,
    toolResults,
    images,
    content: typeof current.text === "string" ? current.text : ""
  };
}
function toKiroChatRequest(input, runtime) {
  const parts = toKiroRequestParts(input);
  const request = buildKiroRequest({
    conversationId: input.conversationId,
    content: parts.content,
    model: input.modelId,
    cwd: input.cwd ?? process.cwd(),
    history: parts.history,
    tools: parts.tools,
    toolResults: parts.toolResults,
    images: parts.images.map((image) => ({
      format: image.format,
      base64: image.source.bytes
    }))
  });
  return normalizeKiroChatRequest(request, runtime ?? {});
}
function toKiroGenerateRequest(input, runtime) {
  const raw = isRecord4(input.request) ? input.request : {};
  const mode = raw.mode === "history" ? "history" : "prompt";
  const systemInstruction = asString(raw.systemInstruction);
  const content = mode === "history" ? [input.reminder, asString(raw.content)].filter(Boolean).join(`

`) : asString(raw.text) ?? "";
  const history = systemInstruction ? [{ role: "system", content: [{ type: "text", text: systemInstruction }] }] : [];
  const locale = mode === "history" ? asString(raw.locale) : undefined;
  return toKiroChatRequest({
    conversationId: input.conversationId,
    modelId: input.modelId,
    history,
    tools: [],
    current: { text: content },
    ...input.cwd ? { cwd: input.cwd } : {},
    ...locale ? { locale } : {},
    ...input.ack ? { ack: input.ack } : {}
  }, runtime);
}

// server/lib/kiro/import-normalize.ts
var KEY_ALIASES = {
  kiroapikey: "apiKey",
  key: "apiKey",
  expired: "expiresAt",
  expiry: "expiresAt",
  issuerurl: "startUrl",
  issuer: "startUrl",
  nickname: "displayName"
};
function snakeToCamelKey(key) {
  if (!/[_\-\s]/.test(key))
    return key;
  const parts = key.split(/[_\-\s]+/).filter(Boolean);
  if (parts.length === 0)
    return key;
  return parts.map((part, i) => i === 0 ? part.toLowerCase() : part[0].toUpperCase() + part.slice(1).toLowerCase()).join("");
}
function canonicalKey(key) {
  const compact = key.trim().toLowerCase().replace(/[_\-\s]/g, "");
  return KEY_ALIASES[compact] ?? snakeToCamelKey(key.trim());
}
function isTruthy(value) {
  if (value == null)
    return false;
  if (typeof value === "string")
    return value.trim() !== "";
  if (typeof value === "number")
    return value !== 0 && !Number.isNaN(value);
  if (typeof value === "boolean")
    return value;
  return true;
}
function isPlainRecord(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
function optionalString(value) {
  if (typeof value === "string") {
    const trimmed = value.trim();
    return trimmed || undefined;
  }
  if (typeof value === "number" && Number.isFinite(value))
    return String(value);
  return;
}
function firstString(...values) {
  for (const value of values) {
    const str = optionalString(value);
    if (str)
      return str;
  }
  return;
}
function canonicalizeRecord(record) {
  const out = {};
  for (const [key, value] of Object.entries(record)) {
    const canonical = canonicalKey(key);
    if (canonical in out && isTruthy(out[canonical]) && !isTruthy(value))
      continue;
    out[canonical] = value;
  }
  return out;
}
function applyApiKeySemantics(cred) {
  const apiKey = firstString(cred.apiKey);
  const refreshToken = firstString(cred.refreshToken);
  const accessToken = firstString(cred.accessToken);
  const declaredApiKey = isApiKeyAuthMethod(optionalString(cred.authMethod));
  if (!declaredApiKey && !(apiKey && !refreshToken))
    return;
  const key = apiKey ?? accessToken;
  if (!key)
    return;
  cred.authMethod = "api_key";
  cred.apiKey = key;
  cred.accessToken = key;
  delete cred.expiresAt;
}
function fromAccount(account) {
  if (!isPlainRecord(account))
    return null;
  const creds = isPlainRecord(account.credentials) ? account.credentials : {};
  const subscription = isPlainRecord(account.subscription) ? account.subscription : {};
  const merged = {
    ...canonicalizeRecord(creds)
  };
  const apiKey = firstString(merged.apiKey, account.kiroApiKey, account.kiro_api_key, account.apiKey);
  if (apiKey)
    merged.apiKey = apiKey;
  const email = firstString(account.email, merged.email);
  if (email)
    merged.email = email;
  const displayName = firstString(account.nickname, merged.displayName);
  if (displayName)
    merged.displayName = displayName;
  const subscriptionTitle = firstString(subscription.title, merged.subscriptionTitle);
  if (subscriptionTitle)
    merged.subscriptionTitle = subscriptionTitle;
  const machineId = firstString(account.machineId, merged.machineId);
  if (machineId)
    merged.machineId = machineId;
  const startUrl = firstString(account.startUrl, merged.startUrl);
  if (startUrl)
    merged.startUrl = startUrl;
  return merged;
}
function normalizeImportCredentials(records) {
  const out = [];
  for (const record of records) {
    if (!isPlainRecord(record))
      continue;
    if (Array.isArray(record.accounts) && !isTruthy(record.credentials)) {
      for (const account of record.accounts) {
        const flat2 = fromAccount(account);
        if (!flat2)
          continue;
        applyApiKeySemantics(flat2);
        out.push(flat2);
      }
      continue;
    }
    if (isTruthy(record.credentials)) {
      const nested = Array.isArray(record.credentials) ? record.credentials : [record.credentials];
      for (const item of nested) {
        if (!isPlainRecord(item))
          continue;
        const flat2 = canonicalizeRecord(item);
        applyApiKeySemantics(flat2);
        out.push(flat2);
      }
      continue;
    }
    const flat = canonicalizeRecord(record);
    applyApiKeySemantics(flat);
    out.push(flat);
  }
  return out;
}
function hasImportableSecret(creds) {
  if (creds.refreshToken?.trim())
    return true;
  return !!creds.apiKey?.trim();
}

// examples/plugins/kiro-external/src/rpc.ts
var RPC_PROTOCOL = "narrafork.rpc/1";
var MAX_HEADER_BYTES = 8 * 1024;
var MAX_FRAME_BYTES = 16 * 1024 * 1024;
var MAX_BUFFER_BYTES = MAX_HEADER_BYTES + MAX_FRAME_BYTES + 4;
var encoder = new TextEncoder;
var decoder = new TextDecoder("utf-8", { fatal: true });
function isRecord5(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
function log2(message, detail) {
  const suffix = detail === undefined ? "" : ` ${safeJson(detail)}`;
  process.stderr.write(`[kiro-external] ${message}${suffix}
`);
}
function safeJson(value) {
  try {
    return JSON.stringify(value) ?? String(value);
  } catch {
    return String(value);
  }
}
function append(left, right) {
  const next = new Uint8Array(new ArrayBuffer(left.byteLength + right.byteLength));
  next.set(left);
  next.set(right, left.byteLength);
  return next;
}
function delimiterIndex(bytes) {
  for (let index = 0;index <= bytes.byteLength - 4; index += 1) {
    if (bytes[index] === 13 && bytes[index + 1] === 10 && bytes[index + 2] === 13 && bytes[index + 3] === 10) {
      return index;
    }
  }
  return -1;
}
function contentLength(header) {
  let length;
  for (const line of header.split(`\r
`)) {
    const separator = line.indexOf(":");
    if (separator <= 0)
      throw new Error("invalid RPC header");
    if (line.slice(0, separator).trim().toLowerCase() !== "content-length")
      continue;
    if (length !== undefined)
      throw new Error("duplicate Content-Length header");
    const value = line.slice(separator + 1).trim();
    if (!/^\d+$/.test(value))
      throw new Error("invalid Content-Length header");
    length = Number(value);
  }
  if (length === undefined || !Number.isSafeInteger(length)) {
    throw new Error("missing Content-Length header");
  }
  return length;
}
function send(message, callback) {
  const body = encoder.encode(JSON.stringify(message));
  const header = encoder.encode(`Content-Length: ${body.byteLength}\r
Content-Type: application/json; charset=utf-8\r
\r
`);
  process.stdout.write(append(header, body), callback);
}
function respond(id, result) {
  send({ jsonrpc: "2.0", id, result });
}
function reject(id, code, message, data) {
  send({
    jsonrpc: "2.0",
    id,
    error: { code, message, ...data === undefined ? {} : { data } }
  });
}
function notify(method, params) {
  send({ jsonrpc: "2.0", method, params });
}
var nextRequestId = 1;
var pendingRequests = new Map;
var HOST_REQUEST_TIMEOUT_MS = 30000;
function request(method, params) {
  const id = `p2h-${nextRequestId++}`;
  return new Promise((resolve3, rejectFn) => {
    const timer = setTimeout(() => {
      pendingRequests.delete(id);
      const error = new Error(`Host request timed out: ${method}`);
      error.code = -32001;
      rejectFn(error);
    }, HOST_REQUEST_TIMEOUT_MS);
    pendingRequests.set(id, { resolve: resolve3, reject: rejectFn, timer });
    send({ jsonrpc: "2.0", id, method, params });
  });
}
function handleResponse(message) {
  if (typeof message.method === "string")
    return false;
  if (!("id" in message))
    return false;
  if (!(("result" in message) || ("error" in message)))
    return false;
  const id = String(message.id);
  const pending = pendingRequests.get(id);
  if (!pending)
    return true;
  clearTimeout(pending.timer);
  pendingRequests.delete(id);
  if ("error" in message && isRecord5(message.error)) {
    const err = message.error;
    const error = new Error(typeof err.message === "string" ? err.message : "Host request failed");
    error.code = typeof err.code === "number" ? err.code : -32603;
    error.data = err.data;
    pending.reject(error);
  } else {
    pending.resolve(message.result);
  }
  return true;
}
function listen(onRequest) {
  let buffer = new Uint8Array(0);
  const parseFrames = () => {
    while (buffer.byteLength > 0) {
      const delimiter = delimiterIndex(buffer);
      if (delimiter < 0) {
        if (buffer.byteLength > MAX_HEADER_BYTES)
          throw new Error("RPC header exceeds limit");
        return;
      }
      if (delimiter > MAX_HEADER_BYTES)
        throw new Error("RPC header exceeds limit");
      const length = contentLength(decoder.decode(buffer.slice(0, delimiter)));
      if (length > MAX_FRAME_BYTES)
        throw new Error("RPC frame exceeds limit");
      const bodyStart = delimiter + 4;
      const frameEnd = bodyStart + length;
      if (buffer.byteLength < frameEnd)
        return;
      const message = JSON.parse(decoder.decode(buffer.slice(bodyStart, frameEnd)));
      buffer = buffer.slice(frameEnd);
      if (!isRecord5(message) || message.jsonrpc !== "2.0")
        continue;
      if (handleResponse(message))
        continue;
      if (typeof message.method === "string" && "id" in message) {
        onRequest(message);
      }
    }
  };
  process.stdin.on("data", (chunk) => {
    try {
      const bytes = new Uint8Array(chunk.byteLength);
      bytes.set(chunk);
      buffer = append(buffer, bytes);
      if (buffer.byteLength > MAX_BUFFER_BYTES)
        throw new Error("RPC input buffer exceeds limit");
      parseFrames();
    } catch (error) {
      log2("fatal framing error", { error: error instanceof Error ? error.message : "unknown" });
      process.exit(2);
    }
  });
  process.stdin.on("end", () => process.exit(0));
}

// examples/plugins/kiro-external/src/credential-vault.ts
var transport = request;
function request2(method, params) {
  return transport(method, params);
}
var CREDENTIAL_KEY_PREFIX = "provider.kiro.cred.";
var INDEX_KEY = "provider.kiro.credentialsIndex";
var LEGACY_BUNDLE_KEY = "provider.kiro.credentialsBundle";
var MIGRATED_BUNDLE_KEY = `${LEGACY_BUNDLE_KEY}.migrated`;
function credentialKey(id) {
  return `${CREDENTIAL_KEY_PREFIX}${id}`;
}
function isCredentialKey(key) {
  return key.startsWith(CREDENTIAL_KEY_PREFIX);
}
async function readKeyState() {
  const result = await request2("secrets.list", {});
  const raw = isRecord5(result) && Array.isArray(result.secrets) ? result.secrets : [];
  const keys = raw.map((item) => isRecord5(item) && typeof item.key === "string" ? item.key : undefined).filter((key) => key !== undefined);
  return {
    credentialKeys: keys.filter(isCredentialKey).sort(),
    hasIndex: keys.includes(INDEX_KEY),
    hasLegacyBundle: keys.includes(LEGACY_BUNDLE_KEY)
  };
}
async function readSecret(key) {
  const result = await request2("secrets.get", { key });
  const value = isRecord5(result) ? result.value : undefined;
  return typeof value === "string" && value.length > 0 ? value : undefined;
}
async function writeSecret(key, value) {
  await request2("secrets.set", { key, value });
}
async function deleteSecret(key) {
  await request2("secrets.delete", { key });
}
async function readIndex() {
  const raw = await readSecret(INDEX_KEY);
  if (!raw)
    return;
  try {
    const parsed = JSON.parse(raw);
    if (!isRecord5(parsed))
      return;
    const revision = typeof parsed.revision === "number" ? parsed.revision : undefined;
    const count = typeof parsed.count === "number" ? parsed.count : undefined;
    if (revision === undefined || count === undefined)
      return;
    return { revision, count };
  } catch {
    log2("credential index is not valid JSON; treating it as absent");
    return;
  }
}
async function writeIndex(index) {
  if (index.count <= 0) {
    await deleteSecret(INDEX_KEY);
    return;
  }
  await writeSecret(INDEX_KEY, JSON.stringify(index));
}
async function readLegacyBundle() {
  return readSecret(LEGACY_BUNDLE_KEY);
}
async function readCredential(key) {
  const raw = await readSecret(key);
  if (!raw)
    return;
  try {
    const parsed = JSON.parse(raw);
    return isRecord5(parsed) ? parsed : undefined;
  } catch {
    log2(`stored credential could not be parsed: ${key}`);
    return;
  }
}
async function readCredentials(keys) {
  const credentials = [];
  for (const key of keys) {
    const credential = await readCredential(key);
    if (credential)
      credentials.push(credential);
  }
  return credentials;
}

// examples/plugins/kiro-external/src/commands.ts
function nextRevision(previous) {
  const now = Date.now();
  return previous !== undefined && previous >= now ? previous + 1 : now;
}
var LOAD_BALANCING_KEY = "provider.kiro.loadBalancingMode";

class CommandInputError extends Error {
  constructor(message) {
    super(message);
    this.name = "CommandInputError";
  }
}
function requireString(value, field) {
  if (typeof value !== "string" || value.trim().length === 0) {
    throw new CommandInputError(`${field} is required`);
  }
  return value.trim();
}
function credentialId(input) {
  if (!isRecord5(input))
    throw new CommandInputError("credentialId is required");
  return requireString(input.credentialId, "credentialId");
}
async function currentBundle(manager, credentialsPath) {
  const { credentials } = loadAllCredentials(credentialsPath);
  if (credentials.length === 0 && manager.snapshot().entries.length > 0) {
    throw new CommandInputError("Credential state could not be read back after the change");
  }
  const writes = [];
  const liveKeys = new Set;
  for (const credential of credentials) {
    if (!credential.id) {
      throw new CommandInputError("A credential has no id and cannot be stored");
    }
    const key = credentialKey(credential.id);
    liveKeys.add(key);
    writes.push({ key, value: JSON.stringify(credential) });
  }
  const state = await readKeyState();
  for (const key of state.credentialKeys) {
    if (!liveKeys.has(key) && isCredentialKey(key))
      writes.push({ key, value: null });
  }
  const revision = nextRevision((await readIndex())?.revision);
  writes.push(credentials.length === 0 ? { key: INDEX_KEY, value: null } : { key: INDEX_KEY, value: JSON.stringify({ revision, count: credentials.length }) });
  return writes;
}
function optionalPositiveInt(value, field, max) {
  if (value === undefined || value === null)
    return;
  if (typeof value !== "number" || !Number.isFinite(value) || value < 1) {
    throw new CommandInputError(`${field} must be a positive number`);
  }
  return Math.min(Math.trunc(value), max);
}
function readSnapshotOptions(input) {
  if (!isRecord5(input))
    return;
  const pageSize = optionalPositiveInt(input.pageSize, "pageSize", 100);
  if (pageSize === undefined)
    return;
  return {
    pageSize,
    paidPage: optionalPositiveInt(input.paidPage, "paidPage", 1e5) ?? 1,
    enterprisePage: optionalPositiveInt(input.enterprisePage, "enterprisePage", 1e5) ?? 1,
    freePage: optionalPositiveInt(input.freePage, "freePage", 1e5) ?? 1
  };
}
function statusOutput(manager, input, credentialsPath) {
  const options = readSnapshotOptions(input);
  const snapshot = manager.snapshot(options);
  const trim = (entries) => entries.map(({ availableModels, ...rest }) => ({
    ...rest,
    modelCount: availableModels?.length ?? 0
  }));
  const visibleIds = new Set(snapshot.entries.map((entry) => entry.id));
  const usageCache = {};
  if (credentialsPath) {
    try {
      for (const [id, entry] of Object.entries(loadUsageCache(credentialsPath))) {
        if (visibleIds.has(id))
          usageCache[id] = entry;
      }
    } catch {}
  }
  return {
    credentialCount: snapshot.total,
    availableCount: snapshot.available,
    total: snapshot.total,
    available: snapshot.available,
    loadBalancingMode: snapshot.loadBalancingMode,
    currentId: snapshot.currentId,
    entries: trim(snapshot.entries),
    paidEntries: trim(snapshot.paidEntries),
    enterpriseEntries: trim(snapshot.enterpriseEntries),
    freeEntries: trim(snapshot.freeEntries),
    paidTotal: snapshot.paidTotal,
    enterpriseTotal: snapshot.enterpriseTotal,
    freeTotal: snapshot.freeTotal,
    unhealthyTotal: snapshot.unhealthyTotal,
    usageCache
  };
}
var COMMAND_HANDLERS = {
  status: async (input, { manager, credentialsPath }) => ({
    output: statusOutput(manager, input, credentialsPath)
  }),
  "credentials.import": async (input, { manager, credentialsPath }) => {
    if (!isRecord5(input))
      throw new CommandInputError("credentials payload is required");
    const raw = input.credentials;
    const records = Array.isArray(raw) ? raw : isRecord5(raw) ? Object.values(raw) : (() => {
      throw new CommandInputError("credentials must be an array or object");
    })();
    const normalized = normalizeImportCredentials(records).filter(hasImportableSecret);
    if (normalized.length === 0) {
      throw new CommandInputError("No importable credentials were found in the payload");
    }
    const result = manager.addCredentials(normalized);
    return {
      output: { added: result.added, duplicates: result.duplicates },
      secretWrites: await currentBundle(manager, credentialsPath)
    };
  },
  "credentials.setState": async (input, { manager, credentialsPath }) => {
    const id = credentialId(input);
    const state = requireString(isRecord5(input) ? input.state : undefined, "state");
    switch (state) {
      case "disabled":
        manager.setDisabled(id, true);
        break;
      case "enabled":
        manager.setDisabled(id, false);
        break;
      case "reset":
        manager.resetAndEnable(id);
        break;
      default:
        throw new CommandInputError(`state must be disabled, enabled or reset: ${state}`);
    }
    return {
      output: statusOutput(manager, input, credentialsPath),
      secretWrites: await currentBundle(manager, credentialsPath)
    };
  },
  "credentials.remove": async (input, { manager, credentialsPath }) => {
    const id = credentialId(input);
    manager.removeCredential(id);
    return {
      output: statusOutput(manager, input, credentialsPath),
      secretWrites: await currentBundle(manager, credentialsPath)
    };
  },
  "credentials.removeMany": async (input, { manager, credentialsPath }) => {
    const raw = isRecord5(input) ? input.credentialIds : undefined;
    if (!Array.isArray(raw) || raw.length === 0) {
      throw new CommandInputError("credentialIds must be a non-empty array");
    }
    if (raw.length > 500)
      throw new CommandInputError("credentialIds exceeds the 500 id limit");
    const ids = raw.map((value, index) => requireString(value, `credentialIds[${index}]`));
    const result = manager.removeCredentials(ids);
    return {
      output: {
        removed: result.removed,
        notFound: result.notFound,
        status: statusOutput(manager, input, credentialsPath)
      },
      secretWrites: await currentBundle(manager, credentialsPath)
    };
  },
  "credentials.removeUnhealthy": async (input, { manager, credentialsPath }) => {
    const result = manager.removeUnhealthyCredentials();
    return {
      output: {
        removed: result.removed,
        reasons: result.reasons,
        status: statusOutput(manager, input, credentialsPath)
      },
      ...result.removed.length > 0 ? { secretWrites: await currentBundle(manager, credentialsPath) } : {}
    };
  },
  "credentials.setPriority": async (input, { manager, credentialsPath }) => {
    const id = credentialId(input);
    const priority = isRecord5(input) ? input.priority : undefined;
    if (typeof priority !== "number" || !Number.isFinite(priority)) {
      throw new CommandInputError("priority must be a number");
    }
    manager.setPriority(id, Math.trunc(priority));
    return {
      output: statusOutput(manager, input, credentialsPath),
      secretWrites: await currentBundle(manager, credentialsPath)
    };
  },
  "credentials.refresh": async (input, { manager, credentialsPath }) => {
    const id = credentialId(input);
    const models = await manager.fetchAndStoreModels(id);
    return {
      output: { credentialId: id, modelCount: models.length },
      secretWrites: await currentBundle(manager, credentialsPath)
    };
  },
  "credentials.update": async (input, { manager, credentialsPath }) => {
    const id = credentialId(input);
    const patch = isRecord5(input) && isRecord5(input.fields) ? input.fields : undefined;
    if (!patch)
      throw new CommandInputError("fields is required");
    const allowed = {};
    const textFields = [
      "email",
      "displayName",
      "region",
      "authRegion",
      "apiRegion",
      "profileArn",
      "subscriptionTitle",
      "startUrl",
      "machineId",
      "proxyUrl",
      "proxyUsername"
    ];
    for (const field of textFields) {
      const value = patch[field];
      if (typeof value === "string")
        allowed[field] = value;
    }
    const secretFields = [
      "refreshToken",
      "apiKey",
      "clientId",
      "clientSecret",
      "proxyPassword"
    ];
    for (const field of secretFields) {
      const value = patch[field];
      if (typeof value === "string" && value.trim().length > 0)
        allowed[field] = value;
    }
    if (typeof patch.authMethod === "string" && patch.authMethod.trim().length > 0) {
      allowed.authMethod = patch.authMethod.trim();
    }
    if (typeof patch.priority === "number" && Number.isFinite(patch.priority)) {
      allowed.priority = Math.trunc(patch.priority);
    }
    if (Object.keys(allowed).length === 0) {
      throw new CommandInputError("No editable fields were provided");
    }
    manager.updateCredential(id, allowed);
    return {
      output: statusOutput(manager, input, credentialsPath),
      secretWrites: await currentBundle(manager, credentialsPath)
    };
  },
  "credentials.usage": async (input, { manager }) => {
    const id = credentialId(input);
    const usage = await manager.getUsageLimits(id);
    return { output: { credentialId: id, usage } };
  },
  "config.setLoadBalancing": async (input, { manager }) => {
    const mode = requireString(isRecord5(input) ? input.mode : undefined, "mode");
    if (mode !== "priority" && mode !== "balanced") {
      throw new CommandInputError(`mode must be priority or balanced: ${mode}`);
    }
    manager.setLoadBalancingMode(mode);
    return {
      output: { loadBalancingMode: mode },
      configWrites: [{ key: LOAD_BALANCING_KEY, value: mode }]
    };
  },
  "verify-credential": async (_input, { manager }) => {
    const result = await manager.listAvailableModels();
    return { output: { ok: true, modelCount: result.models.length } };
  },
  "models.list": async (input, { manager }) => {
    const id = isRecord5(input) ? input.credentialId : undefined;
    const result = await manager.listAvailableModels(id || undefined);
    const models = result.models.map((m) => ({
      id: String(m.model_id ?? m.modelId ?? ""),
      displayName: String(m.display_name ?? m.displayName ?? m.model_id ?? m.modelId ?? ""),
      contextWindow: typeof m.context_window === "number" ? m.context_window : undefined
    }));
    return { output: { credentialId: result.credentialId, models } };
  },
  "models.refresh": async (input, { manager, credentialsPath }) => {
    const id = credentialId(input);
    const modelIds = await manager.fetchAndStoreModels(id);
    return {
      output: { credentialId: id, modelCount: modelIds.length, models: modelIds },
      secretWrites: await currentBundle(manager, credentialsPath)
    };
  },
  "models.refreshAll": async (_input, { manager, credentialsPath }) => {
    const models = await manager.fetchAllCredentialModels();
    return {
      output: { modelCount: models.length },
      secretWrites: await currentBundle(manager, credentialsPath)
    };
  }
};
function findCommand(contributionId) {
  return COMMAND_HANDLERS[contributionId];
}

// examples/plugins/kiro-external/src/credentials.ts
import { mkdirSync as mkdirSync3 } from "fs";
import { tmpdir } from "os";
import { join as join2 } from "path";
class MissingCredentialError extends Error {
  constructor(message) {
    super(message);
    this.name = "MissingCredentialError";
  }
}

class InvalidBundleError extends Error {
  constructor(message) {
    super(message);
    this.name = "InvalidBundleError";
  }
}
function text(value) {
  if (typeof value !== "string")
    return;
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : undefined;
}
function readConfig(raw) {
  if (!isRecord5(raw))
    return {};
  return {
    credentialsBundle: text(raw.credentialsBundle),
    authMethod: text(raw.authMethod),
    refreshToken: text(raw.refreshToken),
    apiKey: text(raw.apiKey),
    clientId: text(raw.clientId),
    clientSecret: text(raw.clientSecret),
    profileArn: text(raw.profileArn),
    region: text(raw.region),
    loadBalancingMode: text(raw.loadBalancingMode)
  };
}
var BUNDLE_TEXT_FIELDS = [
  "id",
  "authMethod",
  "refreshToken",
  "accessToken",
  "apiKey",
  "clientId",
  "clientSecret",
  "profileArn",
  "region",
  "authRegion",
  "apiRegion",
  "expiresAt",
  "machineId",
  "email",
  "displayName",
  "subscriptionTitle",
  "startUrl",
  "disabledReason",
  "proxyUrl",
  "proxyUsername",
  "proxyPassword"
];
function parseBundle(bundle) {
  let parsed;
  try {
    parsed = JSON.parse(bundle);
  } catch {
    throw new InvalidBundleError("Stored Kiro credentials are not valid JSON");
  }
  const entries = Array.isArray(parsed) ? parsed : [parsed];
  const credentials = [];
  for (const entry of entries) {
    if (!isRecord5(entry))
      continue;
    const credential = {};
    for (const field of BUNDLE_TEXT_FIELDS) {
      const value = text(entry[field]);
      if (value !== undefined)
        credential[field] = value;
    }
    if (typeof entry.priority === "number" && Number.isFinite(entry.priority)) {
      credential.priority = Math.trunc(entry.priority);
    }
    if (typeof entry.disabled === "boolean")
      credential.disabled = entry.disabled;
    if (typeof entry.meterUsage === "number" && Number.isFinite(entry.meterUsage)) {
      credential.meterUsage = entry.meterUsage;
    }
    if (Array.isArray(entry.availableModels)) {
      credential.availableModels = entry.availableModels.filter((model) => typeof model === "string");
    }
    if (hasUsableSecret(credential)) {
      credentials.push(credential);
    }
  }
  if (credentials.length === 0) {
    throw new MissingCredentialError("Stored Kiro credentials contain no usable entry");
  }
  return credentials;
}
function hasUsableSecret(credential) {
  const method = canonicalizeAuthMethodValue(credential.authMethod) ?? "social";
  if (method === "api_key")
    return Boolean(credential.apiKey);
  if (!credential.refreshToken)
    return false;
  if (method === "idc")
    return Boolean(credential.clientId && credential.clientSecret);
  return true;
}
function toCredential(config) {
  const authMethod = canonicalizeAuthMethodValue(config.authMethod) ?? "social";
  const missing = [];
  if (authMethod === "api_key") {
    if (!config.apiKey)
      missing.push("apiKey");
  } else {
    if (!config.refreshToken)
      missing.push("refreshToken");
    if (authMethod === "idc") {
      if (!config.clientId)
        missing.push("clientId");
      if (!config.clientSecret)
        missing.push("clientSecret");
    }
  }
  if (missing.length > 0) {
    throw new MissingCredentialError(`Kiro credentials are incomplete for authMethod "${authMethod}": missing ${missing.join(", ")}`);
  }
  const region = config.region ?? "us-east-1";
  return {
    authMethod,
    region,
    authRegion: region,
    apiRegion: region,
    ...config.refreshToken ? { refreshToken: config.refreshToken } : {},
    ...config.apiKey ? { apiKey: config.apiKey } : {},
    ...config.clientId ? { clientId: config.clientId } : {},
    ...config.clientSecret ? { clientSecret: config.clientSecret } : {},
    ...config.profileArn ? { profileArn: config.profileArn } : {}
  };
}
function toCredentials(config) {
  if (config.credentialsBundle)
    return parseBundle(config.credentialsBundle);
  return [toCredential(config)];
}
function pluginDataDir() {
  const base = process.env.NF_PLUGIN_DATA_DIR?.trim();
  const dir = base ? join2(base, "kiro") : join2(tmpdir(), `kiro-external-${process.pid}`);
  try {
    mkdirSync3(dir, { recursive: true, mode: 448 });
  } catch {}
  return dir;
}
function credentialsPathFor(_rawConfig) {
  return join2(pluginDataDir(), "credentials.json");
}
var cached;
function managerConfigKey(config) {
  return `${config.loadBalancingMode ?? ""}\x00${config.region ?? ""}`;
}
async function resolveManager(rawConfig) {
  const config = readConfig(rawConfig);
  const configKey = managerConfigKey(config);
  try {
    return await resolveFromVault(config, configKey);
  } catch (error) {
    if (error instanceof MissingCredentialError || error instanceof InvalidBundleError)
      throw error;
    log2("credential storage is unavailable; falling back to config", {
      error: error instanceof Error ? error.message : "unknown"
    });
    return buildManager(config, toCredentials(config), -1, configKey);
  }
}
async function resolveFromVault(config, configKey) {
  const index = await readIndex();
  if (index) {
    if (cached && cached.revision === index.revision && cached.configKey === configKey) {
      return cached.manager;
    }
    const state = await readKeyState();
    const credentials = await readCredentials(state.credentialKeys);
    if (credentials.length === 0) {
      throw new MissingCredentialError("Kiro credentials are recorded but could not be read. Re-add a credential.");
    }
    return buildManager(config, credentials, index.revision, configKey);
  }
  const migration = await ensureMigrated();
  if (migration.migrated) {
    const state = await readKeyState();
    const credentials = await readCredentials(state.credentialKeys);
    if (credentials.length > 0) {
      const revision = (await readIndex())?.revision ?? -1;
      return buildManager(config, credentials, revision, configKey);
    }
  }
  return buildManager(config, toCredentials(config), -1, configKey);
}
function buildManager(config, credentials, revision, configKey) {
  const credentialsPath = join2(pluginDataDir(), "credentials.json");
  const kiroConfig = loadConfig(join2(pluginDataDir(), "config.json"));
  const region = config.region ?? credentials[0]?.region ?? kiroConfig.region;
  const manager = new MultiTokenManager({
    ...kiroConfig,
    region,
    ...config.loadBalancingMode === "balanced" || config.loadBalancingMode === "priority" ? { loadBalancingMode: config.loadBalancingMode } : {}
  }, credentials, credentialsPath, true);
  cached = { revision, configKey, count: credentials.length, manager };
  log2("credential manager built", { credentialCount: credentials.length, revision });
  return manager;
}
function resetManager() {
  cached = undefined;
}

// examples/plugins/kiro-external/src/credential-migration.ts
var inFlight;
function ensureMigrated() {
  if (!inFlight) {
    inFlight = runMigration().finally(() => {
      inFlight = undefined;
    });
  }
  return inFlight;
}
async function runMigration() {
  const state = await readKeyState();
  if (state.hasIndex) {
    const index = await readIndex();
    if (index && index.count === state.credentialKeys.length) {
      const renamed = state.hasLegacyBundle ? await retireLegacyBundle() : false;
      return { migrated: true, empty: false, count: index.count, changed: renamed };
    }
    log2("credential index disagrees with stored entries; resuming migration", {
      indexCount: index?.count,
      storedEntries: state.credentialKeys.length
    });
  }
  const byId = new Map;
  for (const credential of await readCredentials(state.credentialKeys)) {
    if (credential.id)
      byId.set(credential.id, credential);
  }
  const alreadyStored = new Set(byId.keys());
  if (state.hasLegacyBundle) {
    const blob = await readLegacyBundle();
    if (blob) {
      for (const credential of parseBundleSafely(blob)) {
        if (credential.id && !byId.has(credential.id))
          byId.set(credential.id, credential);
      }
    }
  }
  if (byId.size === 0) {
    return { migrated: false, empty: true, count: 0, changed: false };
  }
  let written = 0;
  for (const [id, credential] of byId) {
    if (alreadyStored.has(id))
      continue;
    await writeSecret(credentialKey(id), JSON.stringify(credential));
    written += 1;
  }
  const after = await readKeyState();
  if (after.credentialKeys.length !== byId.size) {
    throw new Error(`Credential migration did not converge: expected ${byId.size} entries, found ${after.credentialKeys.length}. The original bundle was left untouched.`);
  }
  await writeIndex({ revision: Date.now(), count: byId.size });
  if (state.hasLegacyBundle)
    await retireLegacyBundle();
  log2("credential migration complete", {
    total: byId.size,
    written,
    resumed: alreadyStored.size > 0
  });
  return { migrated: true, empty: false, count: byId.size, changed: true };
}
async function retireLegacyBundle() {
  const blob = await readLegacyBundle();
  if (!blob)
    return false;
  await writeSecret(MIGRATED_BUNDLE_KEY, blob);
  await deleteSecret(LEGACY_BUNDLE_KEY);
  return true;
}
function parseBundleSafely(blob) {
  try {
    return parseBundle(blob);
  } catch (error) {
    log2("legacy credential bundle could not be parsed; migrating what is already stored", {
      error: error instanceof Error ? error.message : "unknown"
    });
    return [];
  }
}

// examples/plugins/kiro-external/src/credential-writeback.ts
import { createHash as createHash3 } from "crypto";
import { readFile } from "fs/promises";
async function credentialFileHash(credentialsPath) {
  try {
    const content = await readFile(credentialsPath, "utf-8");
    return createHash3("sha256").update(content).digest("hex");
  } catch {
    return null;
  }
}
async function writebackIfChanged(credentialsPath, hashBefore, deps) {
  try {
    const hashAfter = await credentialFileHash(credentialsPath);
    if (!hashAfter || hashAfter === hashBefore)
      return;
    const { credentials } = loadAllCredentials(credentialsPath);
    if (credentials.length === 0)
      return;
    await resyncVault(credentials, deps);
  } catch (error) {
    deps.log("credential writeback failed", {
      error: error instanceof Error ? error.message : "unknown"
    });
  }
}
async function resyncVault(credentials, deps) {
  try {
    for (const credential of credentials) {
      if (!credential.id)
        continue;
      await deps.request("secrets.set", {
        key: credentialKey(credential.id),
        value: JSON.stringify(credential)
      });
    }
    const raw = await deps.request("secrets.get", { key: INDEX_KEY });
    const previous = readRevision(raw);
    const now = Date.now();
    await deps.request("secrets.set", {
      key: INDEX_KEY,
      value: JSON.stringify({
        revision: previous !== undefined && previous >= now ? previous + 1 : now,
        count: credentials.length
      })
    });
  } catch (error) {
    deps.log("credential writeback to the vault failed", {
      error: error instanceof Error ? error.message : "unknown"
    });
  }
}
function readRevision(raw) {
  if (typeof raw !== "object" || raw === null)
    return;
  const value = raw.value;
  if (typeof value !== "string" || value.length === 0)
    return;
  try {
    const parsed = JSON.parse(value);
    if (typeof parsed !== "object" || parsed === null)
      return;
    const revision = parsed.revision;
    return typeof revision === "number" ? revision : undefined;
  } catch {
    return;
  }
}

// examples/plugins/kiro-external/src/event-mapping.ts
class UsageAccumulator {
  usage = {};
  dirty = false;
  observe(event) {
    let changed = false;
    if (typeof event.contextUsagePercentage === "number") {
      const pct = Math.min(100, Math.max(0, event.contextUsagePercentage));
      if (this.usage.contextUsagePercentage !== pct) {
        this.usage.contextUsagePercentage = pct;
        changed = true;
      }
    }
    if (event.metering) {
      this.usage.metering = {
        unit: event.metering.unit,
        unitPlural: event.metering.unitPlural,
        usage: event.metering.usage
      };
      changed = true;
    }
    const source = event.usage;
    if (source) {
      for (const key of [
        "promptTokens",
        "inputTokens",
        "completionTokens",
        "cachedInputTokens"
      ]) {
        const value = source[key];
        if (typeof value === "number" && Number.isFinite(value) && value > 0) {
          this.usage[key] = Math.trunc(value);
          changed = true;
        }
      }
    }
    if (changed)
      this.dirty = true;
    return changed;
  }
  setContextWindow(tokens) {
    if (typeof tokens === "number" && tokens > 0) {
      this.usage.contextWindow = Math.trunc(tokens);
      this.dirty = true;
    }
  }
  snapshot() {
    if (!this.dirty)
      return;
    return {
      ...this.usage,
      ...this.usage.metering ? { metering: { ...this.usage.metering } } : {}
    };
  }
}
function reasoningBlockId(outputIndex) {
  return `reasoning-${outputIndex ?? 0}`;
}
function mapKiroEvent(event, usage) {
  const out = [];
  if (typeof event.text === "string" && event.text.length > 0) {
    out.push({
      type: "text.delta",
      text: event.text,
      ...typeof event.textOutputIndex === "number" ? { outputIndex: event.textOutputIndex } : {}
    });
  }
  if (typeof event.reasoning === "string" && event.reasoning.length > 0) {
    const outputIndex = event.reasoningOutputIndex;
    out.push({
      type: "reasoning.delta",
      blockId: reasoningBlockId(outputIndex),
      text: event.reasoning,
      ...typeof outputIndex === "number" ? { outputIndex } : {},
      ...event.reasoningMetadata ? {
        metadata: {
          source: "kiro",
          format: "anthropic",
          data: event.reasoningMetadata
        }
      } : {}
    });
  } else if (event.reasoningMetadata) {
    const outputIndex = event.reasoningOutputIndex;
    out.push({
      type: "reasoning.metadata",
      blockId: reasoningBlockId(outputIndex),
      ...typeof outputIndex === "number" ? { outputIndex } : {},
      metadata: { source: "kiro", format: "anthropic", data: event.reasoningMetadata }
    });
  }
  if (event.redactedThinking?.data) {
    out.push({
      type: "reasoning.redacted",
      data: event.redactedThinking.data,
      ...typeof event.redactedThinking.outputIndex === "number" ? { outputIndex: event.redactedThinking.outputIndex } : {},
      source: "kiro"
    });
  }
  const chunk = event.toolUseChunk;
  if (chunk) {
    if (chunk.name) {
      out.push({
        type: "tool_call.start",
        toolUseId: chunk.toolUseId,
        name: chunk.name,
        ...typeof chunk.outputIndex === "number" ? { outputIndex: chunk.outputIndex } : {}
      });
    }
    if (typeof chunk.input === "string" && chunk.input.length > 0) {
      out.push({
        type: "tool_call.delta",
        toolUseId: chunk.toolUseId,
        argumentsDelta: chunk.input
      });
    }
    if (chunk.stop)
      out.push({ type: "tool_call.end", toolUseId: chunk.toolUseId });
  }
  for (const toolUse of event.toolUses ?? []) {
    out.push({
      type: "tool_call.complete",
      toolUseId: toolUse.toolUseId,
      name: toolUse.name,
      input: toolUse.input,
      ...typeof toolUse.outputIndex === "number" ? { outputIndex: toolUse.outputIndex } : {}
    });
  }
  if (usage.observe(event)) {
    const snapshot = usage.snapshot();
    if (snapshot)
      out.push({ type: "usage", usage: snapshot });
  }
  return out;
}
function classifyKiroError(error) {
  const name = error instanceof Error ? error.name : "";
  const message = error instanceof Error ? error.message : String(error);
  if (name === "AbortError") {
    return { classification: "cancelled", code: "CANCELLED", message: "Request was cancelled" };
  }
  if (name === "KiroQuotaError") {
    return {
      classification: "api",
      code: "QUOTA_EXHAUSTED",
      message,
      retryable: false,
      statusCode: 429
    };
  }
  if (name === "KiroAuthError") {
    return {
      classification: "api",
      code: "AUTH_FAILED",
      message,
      retryable: false,
      statusCode: 401
    };
  }
  if (name === "MissingCredentialError") {
    return { classification: "invalid_state", code: "NOT_CONFIGURED", message, retryable: false };
  }
  if (name === "KiroApiError") {
    const apiCode = error && typeof error === "object" && "code" in error ? error.code : undefined;
    const statusCode = error && typeof error === "object" && "statusCode" in error ? error.statusCode : undefined;
    const numericStatus = typeof statusCode === "number" ? statusCode : undefined;
    switch (apiCode) {
      case "CONTEXT_LENGTH_EXCEEDED":
        return {
          classification: "api",
          code: "CONTEXT_LENGTH_EXCEEDED",
          message,
          retryable: false,
          statusCode: numericStatus
        };
      case "BAD_REQUEST":
        return {
          classification: "api",
          code: "BAD_REQUEST",
          message,
          retryable: false,
          statusCode: numericStatus
        };
      case "AUTH_FAILED":
        return {
          classification: "api",
          code: "AUTH_FAILED",
          message,
          retryable: false,
          statusCode: numericStatus
        };
      case "THROTTLED":
        return {
          classification: "api",
          code: "THROTTLED",
          message,
          retryable: true,
          statusCode: numericStatus
        };
      case "QUOTA_EXHAUSTED":
        return {
          classification: "api",
          code: "QUOTA_EXHAUSTED",
          message,
          retryable: false,
          statusCode: numericStatus
        };
      case "TRANSIENT":
        return {
          classification: "api",
          code: "UPSTREAM_ERROR",
          message,
          retryable: true,
          statusCode: numericStatus
        };
      default:
        return {
          classification: "api",
          code: typeof apiCode === "string" ? apiCode : "UPSTREAM_ERROR",
          message,
          retryable: numericStatus != null ? numericStatus >= 500 || numericStatus === 408 : true,
          statusCode: numericStatus
        };
    }
  }
  return { classification: "transport", code: "REQUEST_FAILED", message, retryable: true };
}

// examples/plugins/kiro-external/src/host-hints.ts
function isRecord6(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
var currentProxyUrl;
function installProxyResolver() {
  registerProxyResolver(() => currentProxyUrl);
}
function applyHostHints(params) {
  if (!isRecord6(params))
    return;
  const hostHints = params.hostHints;
  if (!isRecord6(hostHints))
    return;
  const outbound = hostHints.outbound;
  currentProxyUrl = isRecord6(outbound) && typeof outbound.proxyUrl === "string" && outbound.proxyUrl ? outbound.proxyUrl : undefined;
}

// server/lib/kiro/mcp-client.ts
import { randomUUID as randomUUID3 } from "crypto";
var BASE_DELAY_MS2 = 200;
var MAX_DELAY_MS2 = 2000;
var MAX_THROTTLE_RETRIES2 = 3;
function isAbortError3(err) {
  return err instanceof Error && (err.name === "AbortError" || err.message === "Aborted");
}
function throwIfAborted(signal) {
  if (signal?.aborted)
    throw new Error("Aborted");
}

class KiroMcpClient {
  tokenManager;
  constructor(tokenManager) {
    this.tokenManager = tokenManager;
  }
  async callTool(name, args, signal) {
    const body = {
      id: randomUUID3(),
      jsonrpc: "2.0",
      method: "tools/call",
      params: { name, arguments: args }
    };
    return this.callMcpWithRetry(JSON.stringify(body), signal);
  }
  async listTools(signal) {
    const body = {
      id: randomUUID3(),
      jsonrpc: "2.0",
      method: "tools/list",
      params: {}
    };
    return this.callMcpWithRetry(JSON.stringify(body), signal);
  }
  async callGeneric(method, params, signal) {
    const body = {
      id: randomUUID3(),
      jsonrpc: "2.0",
      method,
      params
    };
    return this.callMcpWithRetry(JSON.stringify(body), signal);
  }
  async callMcpWithRetry(body, signal) {
    const maxRetries = this.tokenManager.maxRetries;
    const failedCredentialIds = new Set;
    let lastError = null;
    const invocationId = randomUUID3();
    const maxAttempts = maxRetries * (MAX_THROTTLE_RETRIES2 + 1);
    let attemptNumber = 0;
    for (let attempt = 0;attempt < maxRetries; attempt++) {
      throwIfAborted(signal);
      let ctx;
      try {
        ctx = await this.tokenManager.acquireContext(null, failedCredentialIds);
      } catch (err) {
        lastError = err instanceof Error ? err : new Error(String(err));
        break;
      }
      throwIfAborted(signal);
      const headers = this.buildHeaders(ctx, invocationId);
      const url = this.buildUrl(ctx);
      const tier = classifyKiroCredentialTier(ctx.credentials);
      let effectiveBody = body;
      if (ctx.credentials.profileArn) {
        const parsed = JSON.parse(body);
        parsed.profileArn = ctx.credentials.profileArn;
        effectiveBody = JSON.stringify(parsed);
      }
      let res;
      const slotState = { held: false };
      try {
        await acquireSlot(ctx.id, tier, ctx.credentials.subscriptionTitle);
        slotState.held = true;
        res = await this.fetchWithThrottleRetry(() => {
          attemptNumber++;
          headers["amz-sdk-request"] = `attempt=${attemptNumber}; max=${maxAttempts}`;
          return pfetch(url, {
            method: "POST",
            headers,
            body: effectiveBody,
            signal
          });
        }, ctx, slotState);
      } catch (err) {
        if (slotState.held)
          releaseSlot(ctx.id);
        if (signal?.aborted || isAbortError3(err))
          throw new Error("Aborted");
        lastError = err instanceof Error ? err : new Error(String(err));
        if (attempt + 1 < maxRetries)
          await this.delay(attempt, signal);
        continue;
      }
      const releaseHeld = () => {
        if (!slotState.held)
          return;
        releaseSlot(ctx.id);
        slotState.held = false;
      };
      if (res.ok) {
        onSuccess(ctx.id);
        releaseHeld();
        this.tokenManager.reportSuccess(ctx.id);
        return await res.json();
      }
      const resBody = await res.text().catch(() => "");
      if (isSuspiciousThrottle(resBody)) {
        onThrottle(ctx.id);
        touchLastRequest(ctx.id);
        recordThrottle(ctx.id);
        releaseHeld();
        const hasFailoverCredential = this.tokenManager.hasAlternativeAvailableCredential(ctx.id, failedCredentialIds);
        if (hasFailoverCredential)
          failedCredentialIds.add(ctx.id);
        this.tokenManager.reportSuspiciousThrottle(ctx.id, failedCredentialIds);
        lastError = new KiroApiError("SUSPICIOUS_THROTTLE", resBody, res.status);
        continue;
      }
      if (res.status === 400) {
        if (isMonthlyRequestLimit(resBody)) {
          releaseHeld();
          failedCredentialIds.add(ctx.id);
          const hasAvailable = this.tokenManager.reportQuotaExhausted(ctx.id);
          if (!hasAvailable)
            throw new KiroQuotaError(resBody);
          lastError = new KiroApiError("QUOTA_EXHAUSTED", resBody, 400);
          continue;
        }
        if (isAccountSuspended(resBody)) {
          releaseHeld();
          failedCredentialIds.add(ctx.id);
          const hasAvailable = this.tokenManager.reportFailure(ctx.id, "account_suspended");
          if (!hasAvailable)
            throw new KiroApiError("ACCOUNT_SUSPENDED", resBody, 400);
          lastError = new KiroApiError("ACCOUNT_SUSPENDED", resBody, 400);
          continue;
        }
        releaseHeld();
        throw new KiroApiError("BAD_REQUEST", resBody, 400);
      }
      if (res.status === 402 && isMonthlyRequestLimit(resBody)) {
        releaseHeld();
        failedCredentialIds.add(ctx.id);
        const hasAvailable = this.tokenManager.reportQuotaExhausted(ctx.id);
        if (!hasAvailable)
          throw new KiroQuotaError(resBody);
        lastError = new KiroApiError("QUOTA_EXHAUSTED", resBody, 402);
        continue;
      }
      if (res.status === 401 || res.status === 403) {
        releaseHeld();
        failedCredentialIds.add(ctx.id);
        const suspended = isAccountSuspended(resBody);
        const hasAvailable = this.tokenManager.reportFailure(ctx.id, suspended ? "account_suspended" : undefined);
        const code = suspended ? "ACCOUNT_SUSPENDED" : "AUTH_FAILED";
        if (!hasAvailable) {
          throw new KiroApiError(code, resBody, res.status);
        }
        lastError = new KiroApiError(code, resBody, res.status);
        continue;
      }
      if (res.status === 429) {
        releaseHeld();
        throw new KiroApiError("THROTTLED", resBody, 429);
      }
      if (res.status === 408 || res.status >= 500) {
        releaseHeld();
        lastError = new KiroApiError("TRANSIENT", resBody, res.status);
        if (attempt + 1 < maxRetries)
          await this.delay(attempt, signal);
        continue;
      }
      releaseHeld();
      throw new KiroApiError("API_ERROR", resBody, res.status);
    }
    throw lastError ?? new Error("MCP: max retries exceeded");
  }
  async fetchWithThrottleRetry(fetchFn, ctx, slotState) {
    const tier = classifyKiroCredentialTier(ctx.credentials);
    for (let i = 0;; i++) {
      recordRequest(ctx.id);
      const response = await fetchFn();
      if (response.status !== 429 || i >= MAX_THROTTLE_RETRIES2)
        return response;
      const bodyText = await response.text().catch(() => "");
      if (isSuspiciousThrottle(bodyText)) {
        return new Response(bodyText, { status: 429, headers: response.headers });
      }
      if (i === 0)
        onThrottle(ctx.id);
      touchLastRequest(ctx.id);
      recordThrottle(ctx.id);
      releaseSlot(ctx.id);
      slotState.held = false;
      await new Promise((resolve3) => setTimeout(resolve3, getBackoffMs(ctx.id)));
      await acquireSlotPriority(ctx.id, tier, ctx.credentials.subscriptionTitle);
      slotState.held = true;
    }
  }
  buildUrl(ctx) {
    const config = this.tokenManager.currentConfig;
    const region = effectiveApiRegion(config, ctx.credentials);
    return `https://q.${region}.amazonaws.com`;
  }
  buildHeaders(ctx, invocationId) {
    const config = this.tokenManager.currentConfig;
    const region = effectiveApiRegion(config, ctx.credentials);
    const { kiroVersion } = config;
    const [os, osVersion] = config.systemVersion.split("#");
    const headers = {
      "Content-Type": "application/x-amz-json-1.0",
      "x-amz-target": "AmazonCodeWhispererStreamingService.InvokeMCP",
      "x-amzn-codewhisperer-optout": "true",
      "User-Agent": `AmazonQ-For-CLI/Version/${kiroVersion}`,
      Host: `q.${region}.amazonaws.com`,
      "amz-sdk-invocation-id": invocationId,
      Authorization: `Bearer ${ctx.token}`,
      "x-amzn-codewhisperer-os": os ?? "linux",
      "x-amzn-codewhisperer-os-version": osVersion ?? "unknown",
      "x-amzn-codewhisperer-ide": "kiro",
      "x-amzn-codewhisperer-ide-version": kiroVersion
    };
    applyKiroTokenTypeHeader(headers, ctx.credentials);
    return headers;
  }
  delay(attempt, signal) {
    const exp = Math.min(BASE_DELAY_MS2 * 2 ** attempt, MAX_DELAY_MS2);
    const jitter = Math.random() * (exp / 4);
    const delayMs = exp + jitter;
    if (!signal)
      return new Promise((resolve3) => setTimeout(resolve3, delayMs));
    throwIfAborted(signal);
    return new Promise((resolve3, reject2) => {
      const onAbort = () => {
        clearTimeout(timer);
        reject2(new Error("Aborted"));
      };
      const timer = setTimeout(() => {
        signal.removeEventListener("abort", onAbort);
        resolve3();
      }, delayMs);
      signal.addEventListener("abort", onAbort, { once: true });
    });
  }
}

// examples/plugins/kiro-external/src/search.ts
function readSearchParams(params) {
  if (!params || typeof params !== "object" || Array.isArray(params)) {
    throw new SearchInputError("provider.search requires an object payload");
  }
  const record = params;
  const query = typeof record.query === "string" ? record.query.trim() : "";
  if (!query)
    throw new SearchInputError("provider.search requires a non-empty query");
  const maxResults = typeof record.maxResults === "number" && Number.isSafeInteger(record.maxResults) ? record.maxResults : undefined;
  return { query, ...maxResults != null ? { maxResults } : {} };
}

class SearchInputError extends Error {
  constructor(message) {
    super(message);
    this.name = "SearchInputError";
  }
}
function searchResultFromMcp(response, query) {
  if (response.error) {
    throw new Error(response.error.message ?? "Kiro MCP search failed");
  }
  const text2 = (response.result?.content ?? []).filter((content) => content.type === "text" && content.text).map((content) => content.text).join(`

`).trim();
  if (response.result?.isError) {
    throw new Error(text2 || "Kiro MCP search reported an error");
  }
  if (!text2) {
    throw new Error(`Kiro MCP search returned no content for: ${query}`);
  }
  return { text: text2 };
}
async function runSearch(config, params, signal) {
  const manager = await resolveManager(config);
  const client = new KiroMcpClient(manager);
  const args = { query: params.query };
  if (params.maxResults != null)
    args.maxResults = params.maxResults;
  const response = await client.callTool("web_search", args, signal);
  return searchResultFromMcp(response, params.query);
}

// examples/plugins/kiro-external/src/stream-failover.ts
function isSuspensionStreamError(error) {
  if (!(error instanceof KiroApiError))
    return false;
  return isSuspendedExceptionType(error.code, error.message);
}
var MAX_FAILOVER_ITERATIONS = 20;
async function* streamWithFailover(initial, options) {
  let chat = initial;
  let iterations = 0;
  while (true) {
    iterations += 1;
    if (iterations > MAX_FAILOVER_ITERATIONS) {
      throw new Error(`streamWithFailover exceeded ${MAX_FAILOVER_ITERATIONS} iterations \u2014 ` + "likely a bug in replay budget tracking or suspension detection");
    }
    const { response, credentialId: credentialId2, release } = chat;
    if (!response.body) {
      release();
      throw new Error("upstream returned no response body");
    }
    const body = response.body;
    let suspension;
    try {
      yield {
        credentialId: credentialId2,
        release,
        events: async function* () {
          try {
            yield* options.parse(body);
          } catch (error) {
            if (!isSuspensionStreamError(error))
              throw error;
            suspension = error;
          }
        }()
      };
    } finally {
      release();
    }
    if (!suspension)
      return;
    const hasAlternative = chat.reportStreamFailure("account_suspended");
    if (options.hasEmittedContent() || !hasAlternative || options.isAborted()) {
      options.log("credential suspended mid-stream; not replaying", {
        emittedContent: options.hasEmittedContent(),
        hasAlternative,
        aborted: options.isAborted()
      });
      throw suspension;
    }
    options.log("credential suspended mid-stream; replaying on another credential");
    chat = await chat.retryWithNextCredential();
  }
}
// server/lib/kiro/event-stream-parser.ts
var PRELUDE_SIZE = 12;
var MIN_MESSAGE_SIZE = PRELUDE_SIZE + 4;
var MAX_MESSAGE_SIZE = 16 * 1024 * 1024;
var DEFAULT_MAX_ERRORS = 5;
var DEFAULT_MAX_BUFFER_SIZE = MAX_MESSAGE_SIZE;
function crc32(data) {
  return Bun.hash.crc32(data);
}
function readU32BE(buf, offset) {
  return (buf[offset] << 24 | buf[offset + 1] << 16 | buf[offset + 2] << 8 | buf[offset + 3]) >>> 0;
}
function readU16BE(buf, offset) {
  return buf[offset] << 8 | buf[offset + 1];
}
function parseHeaders(data, headerLength) {
  const result = {};
  let offset = 0;
  while (offset < headerLength) {
    if (offset >= data.length)
      break;
    const nameLen = data[offset];
    offset += 1;
    if (nameLen === 0 || offset + nameLen > data.length)
      break;
    const name = new TextDecoder().decode(data.subarray(offset, offset + nameLen));
    offset += nameLen;
    if (offset >= data.length)
      break;
    const valueType = data[offset];
    offset += 1;
    switch (valueType) {
      case 0:
      case 1:
        break;
      case 2:
        offset += 1;
        break;
      case 3:
        offset += 2;
        break;
      case 4:
        offset += 4;
        break;
      case 5:
      case 8:
        offset += 8;
        break;
      case 6: {
        if (offset + 2 > data.length)
          return result;
        const len = readU16BE(data, offset);
        offset += 2 + len;
        break;
      }
      case 7: {
        if (offset + 2 > data.length)
          return result;
        const len = readU16BE(data, offset);
        offset += 2;
        if (offset + len > data.length)
          return result;
        const val = new TextDecoder().decode(data.subarray(offset, offset + len));
        offset += len;
        if (name === ":message-type")
          result.messageType = val;
        else if (name === ":event-type")
          result.eventType = val;
        else if (name === ":exception-type")
          result.exceptionType = val;
        else if (name === ":error-code")
          result.errorCode = val;
        break;
      }
      case 9:
        offset += 16;
        break;
      default:
        return result;
    }
  }
  return result;
}
function parseFrameTyped(buffer) {
  if (buffer.length < PRELUDE_SIZE)
    return null;
  const totalLength = readU32BE(buffer, 0);
  const headerLength = readU32BE(buffer, 4);
  const preludeCrc = readU32BE(buffer, 8);
  if (totalLength < MIN_MESSAGE_SIZE) {
    return {
      ok: false,
      error: new KiroParseError("message_too_small", `Message too small: ${totalLength}`)
    };
  }
  if (totalLength > MAX_MESSAGE_SIZE) {
    return {
      ok: false,
      error: new KiroParseError("message_too_large", `Message too large: ${totalLength}`)
    };
  }
  if (headerLength > totalLength - MIN_MESSAGE_SIZE) {
    return {
      ok: false,
      error: new KiroParseError("header_parse_failed", "Header length exceeds frame")
    };
  }
  if (buffer.length < totalLength)
    return null;
  const actualPreludeCrc = crc32(buffer.subarray(0, 8));
  if (actualPreludeCrc !== preludeCrc) {
    return { ok: false, error: new KiroParseError("prelude_crc_mismatch", "Prelude CRC mismatch") };
  }
  const messageCrc = readU32BE(buffer, totalLength - 4);
  const actualMessageCrc = crc32(buffer.subarray(0, totalLength - 4));
  if (actualMessageCrc !== messageCrc) {
    return { ok: false, error: new KiroParseError("message_crc_mismatch", "Message CRC mismatch") };
  }
  const headersStart = PRELUDE_SIZE;
  const headersEnd = headersStart + headerLength;
  const headers = parseHeaders(buffer.subarray(headersStart, headersEnd), headerLength);
  const payload = buffer.slice(headersEnd, totalLength - 4);
  return { ok: true, frame: { headers, payload, consumed: totalLength } };
}

class EventStreamDecoder {
  buffer = new Uint8Array(0);
  state = "ready";
  framesDecoded = 0;
  errorCount = 0;
  maxErrors;
  maxBufferSize;
  bytesSkipped = 0;
  constructor(maxErrors = DEFAULT_MAX_ERRORS, maxBufferSize = DEFAULT_MAX_BUFFER_SIZE) {
    this.maxErrors = maxErrors;
    this.maxBufferSize = maxBufferSize;
  }
  feed(data) {
    const newSize = this.buffer.length + data.length;
    if (newSize > this.maxBufferSize) {
      throw new KiroParseError("buffer_overflow", `Buffer overflow: ${newSize} > ${this.maxBufferSize}`);
    }
    const newBuf = new Uint8Array(newSize);
    newBuf.set(this.buffer);
    newBuf.set(data, this.buffer.length);
    this.buffer = newBuf;
    if (this.state === "recovering")
      this.state = "ready";
  }
  decode() {
    if (this.state === "stopped") {
      throw new KiroParseError("too_many_errors", `Decoder stopped after ${this.errorCount} errors`);
    }
    if (this.buffer.length === 0) {
      this.state = "ready";
      return null;
    }
    this.state = "parsing";
    const result = parseFrameTyped(this.buffer);
    if (result === null) {
      this.state = "ready";
      return null;
    }
    if (result.ok) {
      this.buffer = this.buffer.subarray(result.frame.consumed);
      this.state = "ready";
      this.framesDecoded++;
      this.errorCount = 0;
      return result.frame;
    }
    this.errorCount++;
    if (this.errorCount >= this.maxErrors) {
      this.state = "stopped";
      throw new KiroParseError("too_many_errors", `Too many errors (${this.errorCount}): ${result.error.message}`);
    }
    this.tryRecover(result.error.kind);
    this.state = "recovering";
    return null;
  }
  *decodeAll() {
    while (true) {
      if (this.state === "stopped" || this.state === "recovering")
        return;
      const frame = this.decode();
      if (!frame)
        return;
      yield frame;
    }
  }
  tryResume() {
    if (this.state === "stopped") {
      this.errorCount = 0;
      this.state = "ready";
    }
  }
  reset() {
    this.buffer = new Uint8Array(0);
    this.state = "ready";
    this.framesDecoded = 0;
    this.errorCount = 0;
    this.bytesSkipped = 0;
  }
  get currentState() {
    return this.state;
  }
  get decodedCount() {
    return this.framesDecoded;
  }
  get errors() {
    return this.errorCount;
  }
  get skipped() {
    return this.bytesSkipped;
  }
  get bufferLength() {
    return this.buffer.length;
  }
  tryRecover(kind) {
    if (this.buffer.length === 0)
      return;
    switch (kind) {
      case "prelude_crc_mismatch":
      case "message_too_small":
      case "message_too_large":
        this.buffer = this.buffer.subarray(1);
        this.bytesSkipped++;
        break;
      case "message_crc_mismatch":
      case "header_parse_failed": {
        if (this.buffer.length >= PRELUDE_SIZE) {
          const totalLength = readU32BE(this.buffer, 0);
          if (totalLength >= MIN_MESSAGE_SIZE && totalLength <= this.buffer.length) {
            this.buffer = this.buffer.subarray(totalLength);
            this.bytesSkipped += totalLength;
            break;
          }
        }
        this.buffer = this.buffer.subarray(1);
        this.bytesSkipped++;
        break;
      }
      default:
        this.buffer = this.buffer.subarray(1);
        this.bytesSkipped++;
        break;
    }
  }
}
async function* parseEventStream(body) {
  const reader = body.getReader();
  const decoder2 = new EventStreamDecoder;
  const td = new TextDecoder;
  try {
    while (true) {
      const { done, value } = await readWithTimeout(reader);
      if (done)
        break;
      decoder2.feed(value);
      for (const frame of decoder2.decodeAll()) {
        const { headers, payload } = frame;
        if (headers.messageType === "exception") {
          const msg = td.decode(payload);
          throw new KiroApiError(headers.exceptionType ?? "UnknownException", msg);
        }
        if (headers.messageType === "error") {
          const msg = td.decode(payload);
          throw new KiroApiError(headers.errorCode ?? "UnknownError", msg);
        }
        if (headers.messageType === "event" && headers.eventType) {
          try {
            const data = JSON.parse(td.decode(payload));
            yield { eventType: headers.eventType, data };
          } catch {}
        }
      }
    }
  } finally {
    reader.releaseLock();
  }
}

// examples/plugins/kiro-external/src/wire-stream.ts
async function* parseKiroWireStream(body, options = {}) {
  const toolCallAccumulator = new KiroStreamToolCallAccumulator(options);
  for await (const event of parseEventStream(body)) {
    yield toolCallAccumulator.process(parseKiroStreamEvent(event, { ...options, parseTextToolCalls: false }));
  }
  const trailing = toolCallAccumulator.finalize();
  if (trailing)
    yield trailing;
}

// examples/plugins/kiro-external/src/server.ts
var PLUGIN_ID = "com.narrafork.kiro-external";
var PLUGIN_VERSION = "0.1.2";
var PACKAGE_DIGEST = process.env.NF_PLUGIN_PACKAGE_DIGEST;
var PROVIDER_PROTOCOL = "1.0";
var LOCAL_ID = "kiro";
installProxyResolver();
var writebackDeps = { request, log: log2 };
function writebackIfChanged2(credentialsPath, hashBefore) {
  writebackIfChanged(credentialsPath, hashBefore, writebackDeps);
}
var CONTEXT_WINDOWS = {
  "claude-sonnet-4.5": 200000,
  "claude-sonnet-4": 200000,
  "claude-opus-4.5": 200000,
  "claude-opus-4.6": 200000,
  "claude-haiku-4.5": 200000,
  "deepseek-chat": 64000,
  "deepseek-reasoner": 64000
};
var DEFAULT_CONTEXT_WINDOW = 128000;
function contextWindowFor(modelId) {
  return CONTEXT_WINDOWS[modelId] ?? DEFAULT_CONTEXT_WINDOW;
}
var initialized = false;
var active = false;
var runtimeId;
var generation;
var operations = new Map;
function emit(operationId, event) {
  const operation = operations.get(operationId);
  if (!operation)
    return;
  operation.seq += 1;
  notify("provider.event", {
    protocolVersion: PROVIDER_PROTOCOL,
    operationId,
    seq: operation.seq,
    event
  });
}
function finish(operationId, event) {
  const operation = operations.get(operationId);
  if (!operation)
    return;
  emit(operationId, event);
  operation.release?.();
  operations.delete(operationId);
}
function describeProvider(id, params) {
  const versions = isRecord5(params) && Array.isArray(params.protocolVersions) ? params.protocolVersions : [];
  if (!versions.includes(PROVIDER_PROTOCOL)) {
    reject(id, -32001, "No supported provider protocol version", {
      code: "INCOMPATIBLE",
      offered: versions
    });
    return;
  }
  respond(id, {
    selectedProtocolVersion: PROVIDER_PROTOCOL,
    plugin: { id: PLUGIN_ID, name: "Kiro (External)", version: PLUGIN_VERSION },
    providers: [
      {
        localId: LOCAL_ID,
        displayName: "Kiro (External)",
        description: "Claude and partner models through a Kiro subscription.",
        defaultModelId: "claude-sonnet-4.5",
        configSchema: {
          type: "object",
          properties: {
            credentialsIndex: { type: "string", writeOnly: true, "x-narrafork-secret": true },
            loadBalancingMode: {
              type: "string",
              enum: ["priority", "balanced"],
              default: "priority"
            },
            authMethod: { type: "string", enum: ["social", "idc", "api_key"], default: "social" },
            refreshToken: { type: "string", writeOnly: true, "x-narrafork-secret": true },
            apiKey: { type: "string", writeOnly: true, "x-narrafork-secret": true },
            clientId: { type: "string", writeOnly: true, "x-narrafork-secret": true },
            clientSecret: { type: "string", writeOnly: true, "x-narrafork-secret": true },
            profileArn: { type: "string" },
            region: { type: "string", default: "us-east-1" }
          },
          additionalProperties: false
        },
        capabilities: {
          validateConfig: true,
          listModels: true,
          chat: true,
          generate: true,
          reasoningContinuation: true,
          inputImages: true,
          mayLeakXmlToolCalls: true
        },
        limits: {
          maxConcurrentChat: 2,
          maxConcurrentGenerate: 1,
          maxConfigBytes: 16384,
          maxModelPageSize: 50
        }
      }
    ]
  });
}
async function listModels(id, params) {
  applyHostHints(params);
  const config = isRecord5(params) ? params.config : undefined;
  let manager;
  try {
    manager = await resolveManager(config);
  } catch (error) {
    if (error instanceof MissingCredentialError) {
      log2("listModels called before credentials were configured");
      respond(id, { models: [], stale: true });
      return;
    }
    const classified = classifyKiroError(error);
    reject(id, -32603, classified.message, { code: classified.code });
    return;
  }
  const credPath = credentialsPathFor(config);
  const hashBefore = await credentialFileHash(credPath);
  try {
    const result = await manager.listAvailableModels();
    const models = result.models.map((model) => {
      const modelId = model.modelId ?? model.id ?? model.name;
      return typeof modelId === "string" ? modelId : undefined;
    }).filter((modelId) => Boolean(modelId)).filter((modelId) => modelId !== "auto").map((modelId) => ({
      id: modelId,
      displayName: modelId,
      contextWindow: contextWindowFor(modelId),
      capabilities: {
        chat: true,
        generate: true,
        streaming: true,
        tools: true,
        reasoning: modelId.startsWith("claude-"),
        sessionMode: "stateless"
      }
    }));
    respond(id, { models, catalogVersion: `kiro-${models.length}`, cacheTtlMs: 300000 });
  } catch (error) {
    const classified = classifyKiroError(error);
    reject(id, -32603, classified.message, { code: classified.code });
  } finally {
    writebackIfChanged2(credPath, hashBefore);
  }
}
async function search(id, params) {
  applyHostHints(params);
  let searchParams;
  try {
    searchParams = readSearchParams(params);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    reject(id, -32602, message, { code: "INVALID_PARAMS" });
    return;
  }
  const config = isRecord5(params) ? params.config : undefined;
  const credPath = credentialsPathFor(config);
  const hashBefore = await credentialFileHash(credPath);
  try {
    respond(id, await runSearch(config, searchParams));
  } catch (error) {
    const classified = classifyKiroError(error);
    reject(id, -32603, classified.message, { code: classified.code });
  } finally {
    writebackIfChanged2(credPath, hashBefore);
  }
}
async function validateConfig(id, params) {
  applyHostHints(params);
  const config = isRecord5(params) ? params.config : undefined;
  try {
    await resolveManager(config);
  } catch (error) {
    if (error instanceof MissingCredentialError) {
      respond(id, { valid: false, issues: [{ path: "/", message: error.message }] });
      return;
    }
    throw error;
  }
  const mode = isRecord5(params) && params.mode === "connectivity" ? "connectivity" : "syntax";
  if (mode !== "connectivity") {
    respond(id, { valid: true, issues: [] });
    return;
  }
  try {
    const manager = await resolveManager(config);
    const result = await manager.listAvailableModels();
    respond(id, {
      valid: true,
      issues: [],
      capabilities: { modelCount: result.models.length }
    });
  } catch (error) {
    const classified = classifyKiroError(error);
    respond(id, { valid: false, issues: [{ path: "/", message: classified.message }] });
  }
}
function buildUpstreamRequest(kind, params, operationId, modelId) {
  const request3 = isRecord5(params) ? params.request : undefined;
  const conversation = isRecord5(params) ? params.conversation : undefined;
  const conversationId = isRecord5(conversation) && typeof conversation.conversationId === "string" ? conversation.conversationId : operationId;
  if (kind === "generate") {
    return toKiroGenerateRequest({
      conversationId,
      modelId,
      request: request3,
      reminder: "Reply with ONLY a short title (max 50 chars), nothing else.",
      ack: "Understood. I will generate only a short title. Send me the content."
    });
  }
  const current = isRecord5(request3) ? request3.current : undefined;
  return toKiroChatRequest({
    conversationId,
    modelId,
    history: isRecord5(request3) && Array.isArray(request3.history) ? request3.history : [],
    tools: isRecord5(request3) && Array.isArray(request3.tools) ? request3.tools : [],
    current: isRecord5(current) ? current : {}
  });
}
async function startChat(id, params, kind = "chat") {
  applyHostHints(params);
  const operationId = isRecord5(params) && typeof params.operationId === "string" ? params.operationId : undefined;
  if (!operationId) {
    reject(id, -32602, `${kind} requires an operationId`, { code: "INVALID_PARAMS" });
    return;
  }
  if (operations.has(operationId)) {
    reject(id, -32602, "duplicate operationId", { code: "INVALID_PARAMS" });
    return;
  }
  const chatRequest = isRecord5(params) ? params.request : undefined;
  const config = isRecord5(params) ? params.config : undefined;
  const modelId = isRecord5(params) && typeof params.modelId === "string" ? params.modelId : "";
  const controller = new AbortController;
  const operation = { seq: 0, controller };
  operations.set(operationId, operation);
  respond(id, { accepted: true, operationId });
  const usage = new UsageAccumulator;
  usage.setContextWindow(contextWindowFor(modelId));
  const credPath = credentialsPathFor(config);
  const hashBefore = await credentialFileHash(credPath);
  try {
    const manager = await resolveManager(config);
    const client = new KiroApiClient(manager);
    const kiroRequest = buildUpstreamRequest(kind, params, operationId, modelId);
    const first = await client.sendChatRequest(kiroRequest, controller.signal);
    const hasTools = isRecord5(chatRequest) && Array.isArray(chatRequest.tools) && chatRequest.tools.length > 0;
    let stopReason = "end_turn";
    let reportedStopReason;
    let emittedContent = false;
    let credentialId2 = first.credentialId;
    let invalidStateSeen = false;
    for await (const attempt of streamWithFailover(first, {
      parse: (body) => parseKiroWireStream(body, { parseTextToolCalls: hasTools, model: modelId }),
      hasEmittedContent: () => emittedContent,
      isAborted: () => controller.signal.aborted,
      log: log2
    })) {
      credentialId2 = attempt.credentialId;
      operation.release = attempt.release;
      emit(operationId, { type: "request_started", credentialId: credentialId2 });
      for await (const parsed of attempt.events) {
        if (controller.signal.aborted)
          break;
        if (parsed.invalidState) {
          emit(operationId, { type: "usage", usage: usage.snapshot() ?? {} });
          finish(operationId, {
            type: "done",
            status: "failed",
            stopReason: "error",
            credentialId: credentialId2,
            usage: usage.snapshot()
          });
          manager.reportFailure(credentialId2);
          invalidStateSeen = true;
          return;
        }
        if (parsed.stopReason)
          reportedStopReason = parsed.stopReason;
        for (const event of mapKiroEvent(parsed, usage)) {
          emit(operationId, event);
          if (event.type === "text.delta" || event.type === "reasoning.delta") {
            emittedContent = true;
          }
          if (event.type === "tool_call.complete" || event.type === "tool_call.end") {
            stopReason = "tool_use";
            emittedContent = true;
          }
        }
      }
    }
    if (invalidStateSeen)
      return;
    if (controller.signal.aborted) {
      finish(operationId, { type: "done", status: "cancelled", stopReason: "cancelled" });
      return;
    }
    manager.reportSuccess(credentialId2);
    const finalUsage = usage.snapshot();
    if (finalUsage?.metering)
      manager.addMeterUsage(credentialId2, finalUsage.metering.usage);
    finish(operationId, {
      type: "done",
      status: "completed",
      stopReason: reportedStopReason ?? stopReason,
      credentialId: credentialId2,
      ...finalUsage ? { usage: finalUsage } : {}
    });
  } catch (error) {
    if (error instanceof KiroQuotaError)
      log2("quota exhausted");
    const classified = classifyKiroError(error);
    if (classified.classification === "cancelled") {
      finish(operationId, { type: "done", status: "cancelled", stopReason: "cancelled" });
      return;
    }
    emit(operationId, { type: "error", error: classified });
    finish(operationId, {
      type: "done",
      status: "failed",
      stopReason: "error",
      usage: usage.snapshot()
    });
  } finally {
    writebackIfChanged2(credPath, hashBefore);
  }
}
function cancelOperation(id, params) {
  const operationId = isRecord5(params) && typeof params.operationId === "string" ? params.operationId : undefined;
  if (!operationId) {
    reject(id, -32602, "cancel requires an operationId", { code: "INVALID_PARAMS" });
    return;
  }
  const operation = operations.get(operationId);
  if (!operation) {
    respond(id, { operationId, state: "unknown_operation" });
    return;
  }
  respond(id, { operationId, state: "cancelling" });
  operation.controller.abort();
  finish(operationId, { type: "done", status: "cancelled", stopReason: "cancelled" });
}
async function invokeCommand(id, params) {
  const contributionId = isRecord5(params) ? params.contributionId : undefined;
  if (typeof contributionId !== "string") {
    reject(id, -32602, "commands.invoke requires a contributionId", { code: "INVALID_PARAMS" });
    return;
  }
  const handler = findCommand(contributionId);
  if (!handler) {
    reject(id, -32601, `Unknown command: ${contributionId}`, { code: "METHOD_NOT_FOUND" });
    return;
  }
  const rawInput = isRecord5(params) ? params.input : undefined;
  const config = isRecord5(rawInput) ? rawInput.config : undefined;
  try {
    const manager = await resolveManager(config);
    const result = await handler(rawInput, {
      manager,
      credentialsPath: credentialsPathFor(config)
    });
    respond(id, {
      ...result.output === undefined ? {} : { output: result.output },
      ...result.secretWrites && result.secretWrites.length > 0 ? { secretWrites: result.secretWrites } : {},
      ...result.configWrites && result.configWrites.length > 0 ? { configWrites: result.configWrites } : {}
    });
  } catch (error) {
    if (error instanceof CommandInputError) {
      reject(id, -32602, error.message, { code: "INVALID_PARAMS" });
      return;
    }
    const classified = classifyKiroError(error);
    reject(id, -32603, classified.message, { code: classified.code });
  }
}
function handleRequest(message) {
  const { id, method, params } = message;
  switch (method) {
    case "initialize": {
      const record = isRecord5(params) ? params : {};
      if (record.protocol !== RPC_PROTOCOL || record.pluginId !== PLUGIN_ID) {
        reject(id, -32602, "initialize identity or protocol mismatch", { code: "INVALID_PARAMS" });
        return;
      }
      runtimeId = typeof record.runtimeId === "string" ? record.runtimeId : undefined;
      generation = typeof record.generation === "number" ? record.generation : undefined;
      initialized = true;
      respond(id, { initialized: true, protocol: RPC_PROTOCOL });
      return;
    }
    case "activate":
      if (!initialized) {
        reject(id, -32603, "Plugin must be initialized before activation");
        return;
      }
      active = true;
      ensureMigrated().catch((error) => {
        log2("credential migration failed; it will be retried on the next request", {
          error: error instanceof Error ? error.message : "unknown"
        });
      });
      respond(id, { activated: true });
      return;
    case "health":
      respond(id, {
        healthy: initialized && active,
        status: active ? "ready" : "inactive",
        runtimeId,
        generation
      });
      return;
    case "provider.describe":
      if (!active) {
        reject(id, -32009, "Plugin is not active", { code: "PLUGIN_UNAVAILABLE" });
        return;
      }
      describeProvider(id, params);
      return;
    case "provider.validateConfig":
      validateConfig(id, params).catch((error) => {
        reject(id, -32603, classifyKiroError(error).message, { code: "INTERNAL" });
      });
      return;
    case "provider.listModels":
      listModels(id, params);
      return;
    case "provider.chat":
      if (!active) {
        reject(id, -32009, "Plugin is not active", { code: "PLUGIN_UNAVAILABLE" });
        return;
      }
      startChat(id, params, "chat");
      return;
    case "provider.generate":
      if (!active) {
        reject(id, -32009, "Plugin is not active", { code: "PLUGIN_UNAVAILABLE" });
        return;
      }
      startChat(id, params, "generate");
      return;
    case "provider.search":
      if (!active) {
        reject(id, -32009, "Plugin is not active", { code: "PLUGIN_UNAVAILABLE" });
        return;
      }
      search(id, params);
      return;
    case "provider.cancel":
      cancelOperation(id, params);
      return;
    case "commands.invoke":
      if (!active) {
        reject(id, -32009, "Plugin is not active", { code: "PLUGIN_UNAVAILABLE" });
        return;
      }
      invokeCommand(id, params);
      return;
    case "deactivate":
      active = false;
      for (const operationId of [...operations.keys()]) {
        operations.get(operationId)?.controller.abort();
        finish(operationId, { type: "done", status: "cancelled", stopReason: "cancelled" });
      }
      resetManager();
      respond(id, { deactivated: true });
      return;
    case "shutdown":
      active = false;
      initialized = false;
      for (const operation of operations.values())
        operation.controller.abort();
      operations.clear();
      resetManager();
      send({ jsonrpc: "2.0", id, result: { shutdown: true } }, () => process.exit(0));
      return;
    default:
      reject(id, -32601, `Unknown method: ${method}`, { code: "METHOD_NOT_FOUND" });
  }
}
listen(handleRequest);
send({
  jsonrpc: "2.0",
  method: "hello",
  params: {
    pluginId: PLUGIN_ID,
    version: PLUGIN_VERSION,
    rpcProtocol: RPC_PROTOCOL,
    ...PACKAGE_DIGEST ? { packageDigest: PACKAGE_DIGEST } : {},
    features: ["host_api.notifications", "rpc.cancel", "host_api.requests"]
  }
});
