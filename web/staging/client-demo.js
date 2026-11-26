"use strict";

const queue = document.getElementById("queue");
const connect = document.getElementById("connect");
const sync = document.getElementById("sync");
const feedback = document.getElementById("feedback");
const pending = document.getElementById("pending");
const operations = document.getElementById("operations");
let offline = true;
let operation;

function render() {
  document.getElementById("connection").textContent = offline ? "Offline simulado" : "Online simulado";
  connect.textContent = offline ? "Simular reconexão" : "Simular queda de rede";
  sync.disabled = offline || !operation;
  if (!operation) return;
  pending.textContent = operation === "Pendente" ? "1 operação pendente." : "1 operação sincronizada na simulação.";
  const item = document.createElement("li");
  item.textContent = `Sequência 1 · Atendimento DEMO-001 · ${operation}`;
  operations.replaceChildren(item);
}

queue.addEventListener("click", () => {
  feedback.textContent = operation ? "Atendimento já registrado. A fila continua com uma operação." : "Atendimento registrado na fila de demonstração.";
  operation ??= "Pendente";
  render();
});

connect.addEventListener("click", () => {
  offline = !offline;
  feedback.textContent = offline ? "Queda de rede simulada." : "Reconexão simulada. A fila está disponível para sincronização.";
  render();
});

sync.addEventListener("click", () => {
  feedback.textContent = operation === "Pendente" ? "Sincronização simulada concluída." : "Reenvio reconhecido. Nenhuma operação adicional.";
  operation = "Sincronizada na simulação";
  render();
});
