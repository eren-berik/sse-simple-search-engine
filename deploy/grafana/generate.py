#!/usr/bin/env python3
"""Generates the provisioned Grafana dashboards in dashboards/.

The dashboards are plain JSON that Grafana loads at startup; this script only keeps their
panels consistent. Run it after changing a panel: python3 deploy/grafana/generate.py
"""
import json
import pathlib

DS = {"type": "prometheus", "uid": "prometheus"}
OUT = pathlib.Path(__file__).parent / "dashboards"

# 0 closed, 1 half-open, 2 open (gobreaker's numbering)
CIRCUIT_MAPPINGS = [{
    "type": "value",
    "options": {
        "0": {"text": "Kapalı", "color": "green", "index": 0},
        "1": {"text": "Yarı açık", "color": "yellow", "index": 1},
        "2": {"text": "Açık", "color": "red", "index": 2},
    },
}]


class Layout:
    """Places panels left to right on Grafana's 24-column grid."""

    def __init__(self):
        self.x, self.y, self.row_h, self.next_id = 0, 0, 0, 1

    def place(self, panel, w, h):
        if self.x + w > 24:
            self.x, self.y, self.row_h = 0, self.y + self.row_h, 0
        panel["gridPos"] = {"x": self.x, "y": self.y, "w": w, "h": h}
        panel["id"] = self.next_id
        self.next_id += 1
        self.x += w
        self.row_h = max(self.row_h, h)
        return panel


def targets(*queries):
    return [{"datasource": DS, "expr": expr, "legendFormat": legend, "refId": chr(65 + i)}
            for i, (expr, legend) in enumerate(queries)]


def timeseries(title, *queries, unit="short", description="", stacked=False, bars=False):
    custom = {"lineWidth": 2, "fillOpacity": 10, "showPoints": "never"}
    if stacked:
        custom["stacking"] = {"mode": "normal", "group": "A"}
    if bars:
        custom.update({"drawStyle": "bars", "fillOpacity": 80})
    return {
        "type": "timeseries", "title": title, "description": description, "datasource": DS,
        "targets": targets(*queries),
        "fieldConfig": {"defaults": {"unit": unit, "custom": custom}, "overrides": []},
        "options": {"legend": {"displayMode": "list", "placement": "bottom"},
                    "tooltip": {"mode": "multi", "sort": "desc"}},
    }


def stat(title, expr, unit="short", description="", thresholds=None, decimals=None, mappings=None):
    defaults = {"unit": unit, "color": {"mode": "thresholds"},
                "thresholds": {"mode": "absolute", "steps": thresholds or [{"color": "blue", "value": None}]}}
    if decimals is not None:
        defaults["decimals"] = decimals
    if mappings:
        defaults["mappings"] = mappings
    return {
        "type": "stat", "title": title, "description": description, "datasource": DS,
        "targets": targets((expr, "")),
        "fieldConfig": {"defaults": defaults, "overrides": []},
        "options": {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                    "colorMode": "background", "graphMode": "area", "textMode": "value"},
    }


def circuit_states(title, expr, legend, description=""):
    """One coloured box per breaker with its current state; the sparkline shows recent changes.
    (A state-timeline would be the natural choice, but Grafana 11 ignores its value mappings
    and buckets numeric states by thresholds.)"""
    return {
        "type": "stat", "title": title, "description": description, "datasource": DS,
        "targets": targets((expr, legend)),
        "fieldConfig": {"defaults": {"mappings": CIRCUIT_MAPPINGS, "color": {"mode": "thresholds"},
                                     "thresholds": {"mode": "absolute", "steps": [
                                         {"color": "green", "value": None}, {"color": "yellow", "value": 1},
                                         {"color": "red", "value": 2}]}},
                        "overrides": []},
        "options": {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                    "colorMode": "background", "graphMode": "area", "textMode": "value_and_name",
                    "justifyMode": "center", "orientation": "auto"},
    }


def bargauge(title, expr, legend, description=""):
    return {
        "type": "bargauge", "title": title, "description": description, "datasource": DS,
        "targets": [{**t, "instant": True} for t in targets((expr, legend))],
        "fieldConfig": {"defaults": {"unit": "short", "min": 0, "color": {"mode": "palette-classic"}}, "overrides": []},
        "options": {"orientation": "horizontal", "displayMode": "gradient", "showUnfilled": True,
                    "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False}},
    }


def dashboard(uid, title, description, panels):
    return {
        "uid": uid, "title": title, "description": description, "tags": ["content-search"],
        "timezone": "browser", "schemaVersion": 39, "version": 1, "editable": False,
        "refresh": "10s", "time": {"from": "now-1h", "to": "now"},
        "annotations": {"list": []}, "templating": {"list": []}, "panels": panels,
    }


def write(name, d):
    (OUT / name).write_text(json.dumps(d, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


# Requests to the probes and the dashboard's static files would drown the API traffic.
API = 'route=~"/api.*"'
RED = [{"color": "green", "value": None}, {"color": "red", "value": 1}]


def overview():
    L = Layout()
    panels = [
        L.place(stat("İstek hızı (gateway)", f'sum(rate(http_requests_total{{job="sse-gateway",{API}}}[1m])) or vector(0)',
                     unit="reqps", description="Gateway'e gelen API istekleri, saniye başına."), 6, 4),
        L.place(stat("5xx oranı (gateway)",
                     f'(sum(rate(http_requests_total{{job="sse-gateway",{API},status=~"5.."}}[5m])) or vector(0))'
                     f' / clamp_min(sum(rate(http_requests_total{{job="sse-gateway",{API}}}[5m])), 1e-9)',
                     unit="percentunit", decimals=1,
                     thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 0.01},
                                 {"color": "red", "value": 0.05}]), 6, 4),
        L.place(stat("p95 gecikme (gateway)",
                     f'histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{{job="sse-gateway",{API}}}[5m])))',
                     unit="s",
                     thresholds=[{"color": "green", "value": None}, {"color": "orange", "value": 0.5},
                                 {"color": "red", "value": 2}]), 6, 4),
        L.place(stat("Açık devre", '(count(gateway_circuit_state == 2) or vector(0)) + (count(provider_circuit_state == 2) or vector(0))',
                     description="Açık circuit breaker sayısı: gateway → servis ve sse-service → provider.",
                     thresholds=RED), 6, 4),

        L.place(timeseries("İstek hızı (servis bazında)",
                           (f'sum by (job) (rate(http_requests_total{{{API}}}[1m]))', "{{job}}"), unit="reqps"), 12, 8),
        L.place(timeseries("p95 gecikme (servis bazında)",
                           (f'histogram_quantile(0.95, sum by (job, le) (rate(http_request_duration_seconds_bucket{{{API}}}[5m])))', "{{job}}"),
                           unit="s"), 12, 8),

        L.place(timeseries("Gateway yanıt kodları",
                           (f'sum by (status) (rate(http_requests_total{{job="sse-gateway",{API}}}[1m]))', "{{status}}"),
                           unit="reqps",
                           description="401/403: gateway'deki token ve rol kontrolü; 429: rate limit; 502/503/504: upstream sorunları."), 12, 8),
        L.place(timeseries("Rate limit reddi (kademe bazında)",
                           ('sum by (tier) (increase(gateway_rate_limited_total[1m]))', "{{tier}}"),
                           description="429 ile reddedilen istekler: ip, auth, user, sync.", bars=True), 12, 8),

        L.place(circuit_states("Circuit breaker: gateway → servisler", "gateway_circuit_state", "{{upstream}}",
                               description="Açık devrede istekler servise gitmeden 503 upstream_unavailable alır."), 12, 6),
        L.place(timeseries("Retry ve hızlı ret",
                           ('sum by (upstream) (increase(gateway_upstream_retries_total[1m]))', "retry: {{upstream}}"),
                           ('sum by (upstream) (increase(gateway_circuit_rejected_total[1m]))', "açık devre reddi: {{upstream}}"),
                           bars=True), 12, 6),

        L.place(timeseries("Kimlik doğrulama olayları",
                           ('sum by (event, result) (increase(auth_events_total[5m]))', "{{event}}: {{result}}"),
                           description="invalid_credentials patlaması şifre denemesine, token_reused çalınmış refresh token'a işaret eder.",
                           bars=True), 24, 8),
    ]
    return dashboard("cs-overview", "Content Search · Genel Bakış",
                     "Servislerin trafiği, hataları, gecikmeleri ve gateway'in edge davranışı.", panels)


def search_sync():
    L = Layout()
    panels = [
        L.place(stat("Cache isabet oranı",
                     '(sum(rate(search_requests_total{cache="HIT"}[15m])) or vector(0)) / clamp_min(sum(rate(search_requests_total[15m])), 1e-9)',
                     unit="percentunit", decimals=0, description="Son 15 dakikada Redis'ten karşılanan aramaların oranı.",
                     thresholds=[{"color": "orange", "value": None}, {"color": "green", "value": 0.5}]), 6, 4),
        L.place(stat("Saklanan içerik", 'sum(contents_stored) or vector(0)'), 6, 4),
        L.place(stat("Etkin provider", 'sum(provider_enabled) or vector(0)'), 6, 4),
        L.place(stat("Açık provider devresi", 'count(provider_circuit_state == 2) or vector(0)', thresholds=RED), 6, 4),

        L.place(timeseries("Aramalar (cache durumuna göre)",
                           ('sum by (cache) (rate(search_requests_total[1m]))', "{{cache}}"), unit="reqps", stacked=True,
                           description="HIT: Redis, MISS: veritabanı (sonra cache'lenir), BYPASS: Redis yok."), 12, 8),
        L.place(timeseries("Arama gecikmesi",
                           ('histogram_quantile(0.5, sum by (le) (rate(http_request_duration_seconds_bucket{job="sse-service",route="/api/v1/contents"}[5m])))', "p50"),
                           ('histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{job="sse-service",route="/api/v1/contents"}[5m])))', "p95"),
                           unit="s"), 12, 8),

        L.place(timeseries("Sync çalıştırmaları",
                           ('sum by (provider, status) (increase(sync_runs_total[10m]))', "{{provider}}: {{status}}"),
                           bars=True, description="Periyodik ve manuel sync'ler, provider ve sonuç bazında."), 12, 8),
        L.place(timeseries("Sync süresi (p95)",
                           ('histogram_quantile(0.95, sum by (provider, le) (rate(sync_duration_seconds_bucket[30m])))', "{{provider}}"),
                           unit="s"), 12, 8),

        L.place(circuit_states("Circuit breaker: provider'lar", "provider_circuit_state", "{{provider}}",
                               description="Açık devrede sync o provider'ı atlar; süre dolunca tek bir deneme isteği gönderilir."), 12, 7),
        L.place(bargauge("İçerik sayısı (provider ve tür)", 'sum by (provider, type) (contents_stored)', "{{provider}} · {{type}}"), 12, 7),
    ]
    return dashboard("cs-search-sync", "Content Search · Arama ve Senkronizasyon",
                     "Arama cache'i, provider senkronizasyonu ve provider'ların circuit breaker durumu.", panels)


if __name__ == "__main__":
    OUT.mkdir(exist_ok=True)
    write("overview.json", overview())
    write("search-sync.json", search_sync())
