"use strict";

const queue = document.getElementById("queue");
const connect = document.getElementById("connect");
const sync = document.getElementById("sync");
const feedback = document.getElementById("feedback");
const pending = document.getElementById("pending");
const operations = document.getElementById("operations");
let offline = true;
let operation;
let queuedSelection;

function render() {
  const connection = document.getElementById("connection");
  connection.textContent = offline ? "Offline simulado" : "Online simulado";
  connection.classList.toggle("status-loading", offline);
  connection.classList.toggle("status-online", !offline);
  connect.textContent = offline ? "Simular reconexão" : "Simular queda de rede";
  sync.disabled = offline || !operation;
  if (!operation) return;
  pending.textContent = operation === "Pendente" ? "1 operação pendente." : "1 operação sincronizada na simulação.";
  const item = document.createElement("li");
  item.textContent = ["Sequência 1", "Atendimento DEMO-001", ...queuedSelection, operation].join(" · ");
  operations.replaceChildren(item);
}

queue.addEventListener("click", () => {
  feedback.textContent = operation ? "Atendimento já registrado. A fila continua com uma operação." : "Atendimento registrado na fila de demonstração.";
  if (!operation) {
    queuedSelection = [records.get(attendancePerson.value).name, records.get(attendanceService.value).name];
    operation = "Pendente";
    attendancePerson.disabled = true;
    attendanceService.disabled = true;
  }
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

const screenTitle = document.getElementById("screen-title");
const screenLinks = new Map(
  [...document.querySelectorAll(".store-preview nav a")].map((link) => [link.hash.slice(1), link]),
);
const screens = document.querySelectorAll("[data-screen]");

function showScreen(moveFocus = false) {
  const destination = location.hash.slice(1);
  if (destination === screenTitle.id && moveFocus) return;

  const active = screenLinks.has(destination) ? destination : "painel";
  for (const screen of screens) {
    screen.hidden = screen.dataset.screen !== active;
  }
  for (const [name, link] of screenLinks) {
    if (name === active) link.setAttribute("aria-current", "page");
    else link.removeAttribute("aria-current");
  }
  screenTitle.textContent = screenLinks.get(active).textContent;
  if (moveFocus) {
    feedback.textContent = "";
    screenTitle.focus();
  }
}

showScreen();
window.addEventListener("hashchange", () => showScreen(true));

const recordKinds = {
  person: "Pessoa física",
  company: "Pessoa jurídica",
  deceased: "Falecido",
  catalog: "Catálogo",
};
const fixtures = [
  { id: "P-01", kind: "person", name: "Responsável Família Demo A" },
  { id: "P-02", kind: "person", name: "Responsável Família Demo B" },
  { id: "E-01", kind: "company", name: "Fornecedor Demo A" },
  { id: "D-01", kind: "deceased", name: "Falecido Demo A" },
  { id: "D-02", kind: "deceased", name: "Falecido Demo B" },
  { id: "C-01", kind: "catalog", name: "Serviço de atendimento Demo", service: true },
  { id: "C-02", kind: "catalog", name: "Serviço de traslado Demo", service: true },
  { id: "C-03", kind: "catalog", name: "Produto de floricultura Demo" },
];
const initialRecords = fixtures.filter(({ id }) => id === "P-01" || id === "C-01");
const records = new Map(initialRecords.map((item) => [item.id, item]));
const familyLinks = new Map();
const recordKind = document.getElementById("record-kind");
const recordFixture = document.getElementById("record-fixture");
const recordSearch = document.getElementById("record-search");
const saveRecord = document.getElementById("save-record");
const familyPerson = document.getElementById("family-person");
const familyDeceased = document.getElementById("family-deceased");
const familyRole = document.getElementById("family-role");
const saveFamily = document.getElementById("save-family");
const attendancePerson = document.getElementById("attendance-person");
const attendanceService = document.getElementById("attendance-service");

function populateChoices(control, items, emptyLabel) {
  const previous = control.value;
  const choices = items.map(({ id, name }) => new Option(name, id));
  if (choices.length === 0) choices.push(new Option(emptyLabel, ""));
  control.replaceChildren(...choices);
  if (items.some(({ id }) => id === previous)) control.value = previous;
}

function renderAvailableFixtures() {
  const available = fixtures.filter(({ id, kind }) => kind === recordKind.value && !records.has(id));
  populateChoices(recordFixture, available, "Todos os exemplos deste tipo estão cadastrados");
  recordFixture.disabled = available.length === 0;
  saveRecord.disabled = recordFixture.disabled;
}

function searchable(value) {
  return value.trim().normalize("NFD").replace(/\p{Diacritic}/gu, "").toLocaleLowerCase("pt-BR");
}

function listItem(label) {
  const item = document.createElement("li");
  item.textContent = label;
  return item;
}

function renderRecordList() {
  const filter = searchable(recordSearch.value);
  const matches = [...records.values()].filter(({ name }) => searchable(name).includes(filter));
  document.getElementById("records-list").replaceChildren(
    ...matches.map(({ name, kind }) => listItem(name + " · " + recordKinds[kind])),
  );
  document.getElementById("records-empty").hidden = matches.length > 0;
}

function renderRecordChoices() {
  const saved = [...records.values()];
  const people = saved.filter(({ kind }) => kind === "person");
  populateChoices(familyPerson, people, "Cadastre uma pessoa física");
  populateChoices(familyDeceased, saved.filter(({ kind }) => kind === "deceased"), "Cadastre um falecido");
  saveFamily.disabled = !familyPerson.value || !familyDeceased.value;
  if (!operation) {
    populateChoices(attendancePerson, people, "Cadastre uma pessoa física");
    populateChoices(attendanceService, saved.filter(({ service }) => service), "Cadastre um serviço");
  }
  renderAvailableFixtures();
  renderRecordList();
}

recordKind.addEventListener("change", renderAvailableFixtures);
recordSearch.addEventListener("input", renderRecordList);
saveRecord.addEventListener("click", () => {
  const item = fixtures.find(({ id, kind }) => id === recordFixture.value && kind === recordKind.value);
  if (!item || records.has(item.id)) return;
  records.set(item.id, item);
  recordSearch.value = "";
  renderRecordChoices();
  feedback.textContent = item.name + " cadastrado na demonstração.";
});

saveFamily.addEventListener("click", () => {
  const person = records.get(familyPerson.value);
  const deceased = records.get(familyDeceased.value);
  if (person?.kind !== "person" || deceased?.kind !== "deceased") return;
  const key = person.id + "/" + deceased.id;
  if (familyLinks.has(key)) {
    feedback.textContent = "Já existe um vínculo entre esses dois cadastros.";
    return;
  }
  familyLinks.set(key, deceased.name + " · " + person.name + " · " + familyRole.value);
  document.getElementById("family-list").replaceChildren(...[...familyLinks.values()].map(listItem));
  feedback.textContent = "Vínculo registrado na demonstração.";
});

renderRecordChoices();
