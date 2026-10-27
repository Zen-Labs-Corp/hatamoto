// k6 load test for the public hatamoto.dev paths (G9). NOT for production:
// point it at a staging copy or a local instance you own.
//
//   k6 run -e BASE_URL=https://staging.example.test deploy/monitor/loadtest.k6.js
//   k6 run -e BASE_URL=http://127.0.0.1:8090 -e VUS=50 -e DURATION=2m deploy/monitor/loadtest.k6.js
//
// Scenarios (shares of traffic): site pages 60% (home, docs, changelog, plugins),
// update/registry files 40% (channel manifests, registry.json, go-import).
// Thresholds fail the run when p95 > 500 ms (pages) / 200 ms (static) or errors > 0.5%.
import http from 'k6/http';
import { check } from 'k6';

const BASE = (__ENV.BASE_URL || '').replace(/\/$/, '');
if (!BASE) throw new Error('set -e BASE_URL=https://staging-host (never production)');
if (/^https?:\/\/(www\.)?hatamoto\.dev(:\d+)?(\/|$)/i.test(BASE) && !__ENV.I_OWN_THIS) {
  throw new Error('refusing to load-test production hatamoto.dev');
}

export const options = {
  scenarios: {
    pages: { executor: 'constant-vus', exec: 'pages', vus: Math.ceil((+__ENV.VUS || 40) * 0.6), duration: __ENV.DURATION || '1m' },
    static: { executor: 'constant-vus', exec: 'staticFiles', vus: Math.ceil((+__ENV.VUS || 40) * 0.4), duration: __ENV.DURATION || '1m' },
  },
  thresholds: {
    'http_req_failed': ['rate<0.005'],
    'http_req_duration{kind:page}': ['p(95)<500'],
    'http_req_duration{kind:static}': ['p(95)<200'],
  },
};

const pagePaths = ['/', '/docs', '/changelog', '/plugins'];
const staticPaths = ['/releases/channels/stable.json', '/releases/channels/edge.json', '/registry.json', '/sdk?go-get=1'];

export function pages() {
  const p = pagePaths[Math.floor(Math.random() * pagePaths.length)];
  const r = http.get(BASE + p, { tags: { kind: 'page' }, headers: { 'Accept-Encoding': 'gzip' } });
  check(r, { 'page 200': (x) => x.status === 200 });
}

export function staticFiles() {
  const p = staticPaths[Math.floor(Math.random() * staticPaths.length)];
  const r = http.get(BASE + p, { tags: { kind: 'static' } });
  check(r, { 'static 200': (x) => x.status === 200 });
}
