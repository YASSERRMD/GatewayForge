import http from 'k6/http';
import { check } from 'k6';

export const options = {
  vus: Number(__ENV.VUS || 32),
  duration: __ENV.DURATION || '10s',
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<100', 'p(99)<250'],
  },
};

export default function () {
  const baseUrl = __ENV.TARGET_URL || 'http://127.0.0.1:18081';
  const host = __ENV.TARGET_HOST || 'localhost';
  const path = __ENV.TARGET_PATH || '/echo';

  const res = http.get(`${baseUrl}${path}`, {
    headers: { Host: host },
  });

  check(res, {
    'status is 200': (r) => r.status === 200,
    'has X-Request-Id': (r) => Boolean(r.headers['X-Request-Id']),
  });
}
