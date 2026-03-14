import http from 'k6/http';
import { check, sleep } from 'k6';

export let options = {
    stages: [
        { duration: '10s', target: 10 },   // Warmup: 10 VUs
        { duration: '20s', target: 50 },   // Ramp up: 50 VUs
        { duration: '30s', target: 50 },   // Steady: 50 VUs
        { duration: '10s', target: 0 },    // Ramp down
    ],
    thresholds: {
        http_req_duration: ['p(95)<500'],  // 95% of requests should be < 500ms
        http_req_failed: ['rate<0.01'],     // Error rate should be < 1%
    },
};

export default function () {
    // Test /api/data endpoint (5% slow responses)
    let res = http.get('http://localhost:5000/api/data', {
        tags: { name: 'api_data' },
    });

    check(res, {
        'status is 200': (r) => r.status === 200,
        'has message': (r) => JSON.parse(r.body).message !== undefined,
    });

    // Test /api/db/{id} endpoint
    let dbRes = http.get(`http://localhost:5000/api/db/${Math.floor(Math.random() * 100)}`, {
        tags: { name: 'api_db' },
    });

    check(dbRes, {
        'db status is 200': (r) => r.status === 200,
    });

    sleep(0.1);  // 100ms between iterations = 10 req/sec per VU
}

// Summary function to show results
export function handleSummary(data) {
    return {
        'stdout': textSummary(data),
    };
}

function textSummary(data) {
    let summary = '\n========== Load Test Summary ==========\n\n';
    summary += `Total Requests: ${data.metrics.http_reqs.values.count}\n`;
    summary += `Failed Requests: ${data.metrics.http_req_failed.values.passes}\n`;
    summary += `Avg Response Time: ${data.metrics.http_req_duration.values.avg.toFixed(2)}ms\n`;
    summary += `P95 Response Time: ${data.metrics.http_req_duration.values['p(95)'].toFixed(2)}ms\n`;
    summary += `P99 Response Time: ${data.metrics.http_req_duration.values['p(99)'].toFixed(2)}ms\n`;
    summary += '\n==========================================\n';
    return summary;
}
