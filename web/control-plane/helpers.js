(function (root) {
  "use strict";
  const helpers = {
    escapeHtml(value) {
      return String(value ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#039;" }[c]));
    },
    maskCnpj(value) {
      if (!value) return "mascarado";
      const digits = String(value).replace(/\D/g, "");
      if (digits.length < 4) return "**********" + String(value).slice(-4);
      return "*".repeat(Math.max(0, digits.length - 4)) + digits.slice(-4);
    },
    displayDate(value) {
      if (!value) return "sem validade final";
      const d = new Date(value);
      if (Number.isNaN(d.getTime())) return "data invalida";
      return new Intl.DateTimeFormat("pt-BR", { dateStyle: "medium" }).format(d);
    },
    errorText(error) {
      return error && error.message ? error.message : "Nao foi possivel concluir a consulta.";
    },
    normalizeError(payload, status) {
      return {
        code: (payload && payload.code) || "HTTP_" + status,
        message: (payload && payload.message) || "Falha HTTP " + status,
        traceId: (payload && payload.traceId) || null,
        details: (payload && payload.details) || {},
      };
    },
    pageState(items, loading, error) {
      if (loading) return "loading";
      if (error) return "error";
      if (!items || items.length === 0) return "empty";
      return "success";
    },
    buildIdempotencyKey(intent, module, seed) {
      const base = String(intent || "op").replace(/[^a-z0-9-]/gi, "-").toLowerCase();
      const mod = String(module || "module").replace(/[^a-z0-9-]/gi, "-").toLowerCase();
      const nonce = String(seed || Date.now()) + "-" + Math.random().toString(36).slice(2, 10);
      const key = `${base}-${mod}-${nonce}`;
      return key.length <= 200 ? key : key.slice(0, 200);
    },
    parseVersion(value) {
      const n = Number(value);
      return Number.isFinite(n) ? n : 0;
    },
    formatError(error) {
      if (!error) return "";
      const code = error.code || "ERRO";
      const msg = error.message || "Falha desconhecida";
      const trace = error.traceId ? ` (rastreio ${error.traceId})` : "";
      return `${code}: ${msg}${trace}`;
    },
  };
  if (typeof module !== "undefined" && module.exports) module.exports = helpers;
  root.AmpariusHelpers = helpers;
})(typeof globalThis !== "undefined" ? globalThis : this);
