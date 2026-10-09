// The key-holding pilot has no gateway, author storage, registry or cron code.
// Its hostname remains reserved when disabled or if configuration is missing.
import { mobileError, mobileOriginConfig, serveMobile } from "./mobile.js";

export default {
  async fetch(request, env) {
    try {
      const config = mobileOriginConfig(env);
      if (!env.MOBILE_ORIGIN || !config.origin || config.origin !== env.MOBILE_ORIGIN || config.host === config.defaultHost) {
        return mobileError(503, "mobile_unconfigured", "Phone publishing has not been configured on this host.", request.method);
      }
      const url = new URL(request.url);
      if (url.origin !== config.origin) {
        return mobileError(404, "not_found", "Not found.", request.method);
      }
      return await serveMobile(request, url, env, config, { requireProxySecret: true, requireRateLimits: true });
    } catch {
      // Do not log exception messages, requests, credentials or pairing URLs.
      return mobileError(503, "mobile_unavailable", "Phone publishing is temporarily unavailable. Your draft is still saved.", request.method);
    }
  },
};
