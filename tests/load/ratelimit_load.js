import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';

const allowedReqs = new Counter('gateway_allowed_requests');
const limitedReqs = new Counter('gateway_rate_limited_requests');
const failClosedReqs = new Counter('gateway_fail_closed_requests');

export const options = {
  vus: Number(__ENV.VUS || 20),
  duration: __ENV.DURATION || '10s',
  thresholds: {
    http_req_duration: ['p(95)<150'],
  },
};

export default function () {
  const baseUrl = __ENV.GATEWAY_URL || 'http://127.0.0.1:18081';
  const host = __ENV.GATEWAY_HOST || 'bench.gateway.test';
  const path = __ENV.RATE_LIMIT_PATH || '/limited/echo';

  const res = http.get(`${baseUrl}${path}`, {
    headers: { Host: host },
  });

  if (res.status === 200) {
    allowedReqs.add(1);
  } else if (res.status === 429) {
    limitedReqs.add(1);
    check(res, {
      '429 includes Retry-After': (r) => Boolean(r.headers['Retry-After']),
      '429 includes RateLimit': (r) => Boolean(r.headers['Ratelimit'] || r.headers['RateLimit']),
    });
  } else if (res.status === 503) {
    failClosedReqs.add(1);
  }

  check(res, {
    'status is expected (200, 429, or 503)': (r) =>
      r.status === 200 || r.status === 429 || r.status === 503,
  });
}
