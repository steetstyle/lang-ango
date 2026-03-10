import http from 'k6/http';
import { check, sleep, group } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';

const BASE_URL = __ENV.APP_URL || 'http://localhost:5000';
const AGENT_ENABLED = __ENV.AGENT_ENABLED === 'true';

const errorRate = new Rate('errors');
const requestDuration = new Trend('request_duration_ms');
const sqlQueryDuration = new Trend('sql_query_duration_ms');
const httpStatus500 = new Counter('http_status_500');

const scenarios = {
  smoke: {
    executor: 'constant-vus',
    vus: 1,
    duration: '30s',
  },
  load: {
    executor: 'ramping-vus',
    startVUs: 0,
    stages: [
      { duration: '30s', target: 10 },
      { duration: '1m', target: 10 },
      { duration: '30s', target: 0 },
    ],
  },
  stress: {
    executor: 'ramping-vus',
    startVUs: 0,
    stages: [
      { duration: '30s', target: 50 },
      { duration: '1m', target: 50 },
      { duration: '30s', target: 0 },
    ],
  },
};

export const options = {
  scenarios,
  thresholds: {
    errors: ['rate<0.01'],
    http_req_duration: ['p(95)<500'],
    request_duration_ms: ['p(95)<200'],
  },
};

export default function () {
  const traceparent = `00-${generateTraceID()}-${generateSpanID()}-01`;

  group('Auto.API Endpoint Tests', () => {
    const response = http.get(`${BASE_URL}/api/auto/auto-information`, {
      headers: {
        'Content-Type': 'application/json',
        'traceparent': traceparent,
        'X-Request-ID': `k6-${__VU}-${__ITER}`,
      },
    });

    requestDuration.add(response.timings.duration);

    check(response, {
      'status is 200 or 500': (r) => [200, 500].includes(r.status),
      'response has traceparent': (r) => r.headers['traceparent'] !== undefined,
    }) || errorRate.add(1);

    if (response.status === 500) {
      httpStatus500.add(1);
    }
  });

  group('SQL Query Detection', () => {
    const response = http.get(`${BASE_URL}/api/auto/auto-information`, {
      headers: {
        'Content-Type': 'application/json',
        'traceparent': traceparent,
      },
    });

    sqlQueryDuration.add(response.timings.duration);
  });

  if (!AGENT_ENABLED) {
    group('Baseline Without Agent', () => {
      const response = http.get(`${BASE_URL}/api/auto/auto-information`);
      requestDuration.add(response.timings.duration);
    });
  }
}

function generateTraceID() {
  const chars = '0123456789abcdef';
  let traceId = '';
  for (let i = 0; i < 32; i++) {
    traceId += chars[Math.floor(Math.random() * chars.length)];
  }
  return traceId;
}

function generateSpanID() {
  const chars = '0123456789abcdef';
  let spanId = '';
  for (let i = 0; i < 16; i++) {
    spanId += chars[Math.floor(Math.random() * chars.length)];
  }
  return spanId;
}

export function handleSummary(data) {
  const summary = {
    'Test Summary': {
      'Total Requests': data.metrics.http_reqs.values.count,
      'Failed Requests': data.metrics.http_req_failed.values.passes,
      'Avg Duration (ms)': data.metrics.http_req_duration.values.avg.toFixed(2),
      'P95 Duration (ms)': data.metrics.http_req_duration.values['p(95)'].toFixed(2),
      'Max Duration (ms)': data.metrics.http_req_duration.values.max.toFixed(2),
      'HTTP Status 500': data.metrics.http_status_500?.values || 0,
    },
  };

  if (AGENT_ENABLED) {
    summary['Agent Overhead'] = {
      'SQL Query Duration P95 (ms)': data.metrics.sql_query_duration_ms?.values['p(95)']?.toFixed(2) || 'N/A',
    };
  }

  return {
    'stdout': JSON.stringify(summary, null, 2),
    'tests/e2e/results.json': JSON.stringify(data, null, 2),
  };
}
