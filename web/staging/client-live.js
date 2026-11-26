/* Modo operacional da loja (cliente web privado).
 *
 * Quando `window.AMPARIUS_CONFIG` está presente, substitui o fluxo sintético
 * de Cadastros e Atendimento por chamadas reais à API (client-api.js) e
 * expõe os estados de vazio, erro, 401, 403 e offline. Sem configuração, este
 * script não faz nada e a prévia sintética de demonstração permanece.
 */
(() => {
  "use strict";
  const api = window.AmpariusClientAPI;
  if (!api || !api.configured) return;

  const state = { parties: [], deceased: [], services: [] };

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function option(value, label) {
    const node = document.createElement("option");
    node.value = value;
    node.textContent = label;
    return node;
  }

  const connection = document.getElementById("connection");
  function setConnection(text, kind) {
    connection.textContent = text;
    connection.className = "status " + kind;
  }
  setConnection(api.mode === "dev" ? "Online (ambiente privado)" : "Online autenticado", "status-online");

  const feedback = document.getElementById("feedback");
  function setFeedback(text) {
    feedback.textContent = text;
  }

  function reportError(err) {
    if (api.isOfflineError(err)) {
      setConnection("Offline", "status-loading");
      setFeedback("Sem conexão com o servidor. A alteração não foi enviada e pode ser repetida depois.");
      return;
    }
    if (err.status === 401) {
      setConnection("Não autenticado", "status-loading");
      setFeedback("Sessão expirada ou ausente. Entre novamente para continuar.");
      return;
    }
    if (err.status === 403) {
      setFeedback("Sem permissão para esta ação no seu papel.");
      return;
    }
    setFeedback("Erro: " + (err.message || "falha inesperada."));
  }

  // ---------- Cadastros ----------
  const cadastros = document.querySelector('[data-screen="cadastros"]');
  if (cadastros) {
    cadastros.innerHTML = "";
    cadastros.appendChild(el("section", "state-panel demo-section", null));

    const wrap = cadastros.firstElementChild;
    wrap.appendChild(el("h2", null, "Cadastros de loja (operacional)"));
    wrap.appendChild(el("p", "muted", "Dados persistidos pela API. Busca e cadastro usam o contexto do ambiente."));

    const personForm = el("div", "store-fields", null);
    const personLabel = el("label", null, null);
    personLabel.append("Pessoa física");
    const personName = el("input", null, null);
    personName.type = "text";
    personName.id = "live-person-name";
    personName.maxLength = 200;
    personName.placeholder = "Nome civil";
    personLabel.appendChild(personName);
    personForm.appendChild(personLabel);
    const personCreate = el("button", null, "Cadastrar pessoa física");
    personCreate.type = "button";
    personCreate.id = "live-person-create";
    wrap.appendChild(personForm);
    wrap.appendChild(personCreate);

    const searchLabel = el("label", "store-search", null);
    searchLabel.append("Buscar responsável por nome");
    const search = el("input", null, null);
    search.type = "search";
    search.id = "live-search";
    search.autocomplete = "off";
    search.maxLength = 200;
    search.placeholder = "Ex.: família";
    searchLabel.appendChild(search);
    wrap.appendChild(searchLabel);

    const empty = el("p", null, "Nenhum cadastro encontrado.");
    empty.id = "live-records-empty";
    empty.hidden = true;
    empty.setAttribute("role", "status");
    wrap.appendChild(empty);
    const list = el("ul", "store-list", null);
    list.id = "live-records-list";
    wrap.appendChild(list);

    // Falecido
    const deceasedForm = el("div", "store-fields", null);
    const deceasedLabel = el("label", null, null);
    deceasedLabel.append("Pessoa a registrar como falecido");
    const deceasedPerson = el("select", null, null);
    deceasedPerson.id = "live-deceased-person";
    deceasedLabel.appendChild(deceasedPerson);
    deceasedForm.appendChild(deceasedLabel);
    wrap.appendChild(deceasedForm);
    const deceasedCreate = el("button", null, "Registrar falecido");
    deceasedCreate.type = "button";
    deceasedCreate.id = "live-deceased-create";
    wrap.appendChild(deceasedCreate);

    // Vínculo familiar
    wrap.appendChild(el("h2", null, "Vínculos familiares"));
    const familyForm = el("div", "store-fields", null);
    const fpLabel = el("label", null, null);
    fpLabel.append("Pessoa física");
    const fp = el("select", null, null);
    fp.id = "live-family-person";
    fpLabel.appendChild(fp);
    const fdLabel = el("label", null, null);
    fdLabel.append("Falecido");
    const fd = el("select", null, null);
    fd.id = "live-family-deceased";
    fdLabel.appendChild(fd);
    const frLabel = el("label", null, null);
    frLabel.append("Parentesco");
    const fr = el("select", null, null);
    fr.id = "live-family-relation";
    fr.replaceChildren(
      option("child", "Filho(a)"),
      option("spouse", "Cônjuge"),
      option("sibling", "Irmão(ã)"),
      option("other", "Outro"),
    );
    frLabel.appendChild(fr);
    familyForm.append(fpLabel, fdLabel, frLabel);
    wrap.appendChild(familyForm);
    const familyCreate = el("button", null, "Vincular familiar");
    familyCreate.type = "button";
    familyCreate.id = "live-family-create";
    wrap.appendChild(familyCreate);
    const familyList = el("ul", "store-list", null);
    familyList.id = "live-family-list";
    wrap.appendChild(familyList);

    const renderParties = (items) => {
      state.parties = items;
      list.replaceChildren(...items.map((p) => el("li", null, p.name + " — Pessoa física")));
      empty.hidden = items.length > 0;
      deceasedPerson.replaceChildren(...items.map((p) => option(p.id, p.name)));
      fp.replaceChildren(...items.map((p) => option(p.id, p.name)));
      refreshAttendance();
    };

    search.addEventListener("input", async () => {
      try {
        renderParties(await api.searchParties(search.value));
        if (search.value.trim() === "") setFeedback("");
      } catch (err) {
        reportError(err);
        empty.hidden = false;
      }
    });

    personCreate.addEventListener("click", async () => {
      const name = personName.value.trim();
      if (!name) {
        setFeedback("Informe um nome para cadastrar.");
        return;
      }
      try {
        await api.createParty({ name });
        setFeedback("Pessoa física cadastrada.");
        personName.value = "";
        renderParties(await api.searchParties(name.slice(0, 24)));
      } catch (err) {
        reportError(err);
      }
    });

    deceasedCreate.addEventListener("click", async () => {
      const personId = deceasedPerson.value;
      if (!personId) {
        setFeedback("Selecione uma pessoa física para registrar o falecido.");
        return;
      }
      try {
        const created = await api.createDeceased({ personId, deathAt: new Date().toISOString() });
        state.deceased.push(created);
        fd.replaceChildren(...state.deceased.map((d) => option(d.id, d.id)));
        setFeedback("Falecido registrado.");
      } catch (err) {
        reportError(err);
      }
    });

    familyCreate.addEventListener("click", async () => {
      if (!fp.value || !fd.value) {
        setFeedback("Selecione pessoa física e falecido para vincular.");
        return;
      }
      try {
        await api.createFamilyLink({ deceasedId: fd.value, personId: fp.value, relation: fr.value });
        const linkLabel = fr.value + " -> " + fd.value;
        familyList.appendChild(el("li", null, linkLabel));
        setFeedback("Vínculo registrado.");
      } catch (err) {
        reportError(err);
      }
    });
  }

  // ---------- Atendimento ----------
  const atendimento = document.querySelector('[data-screen="atendimento"]');
  function refreshAttendance() {
    if (!atendimento) return;
    const person = document.getElementById("live-att-person");
    if (person) person.replaceChildren(...state.parties.map((p) => option(p.id, p.name)));
  }

  if (atendimento) {
    atendimento.innerHTML = "";
    atendimento.appendChild(el("section", "state-panel demo-section", null));
    const wrap = atendimento.firstElementChild;
    wrap.appendChild(el("h2", null, "Atendimento funeral"));
    wrap.appendChild(el("p", "muted", "Responsável e serviço reais, resolvidos pela API."));

    const personLabel = el("label", null, null);
    personLabel.append("Responsável");
    const person = el("select", null, null);
    person.id = "live-att-person";
    personLabel.appendChild(person);
    const serviceSearchLabel = el("label", null, null);
    serviceSearchLabel.append("Buscar serviço");
    const serviceSearch = el("input", null, null);
    serviceSearch.type = "search";
    serviceSearch.id = "live-att-service-search";
    serviceSearch.placeholder = "Ex.: missa, traslado";
    serviceSearchLabel.appendChild(serviceSearch);
    const serviceLabel = el("label", null, null);
    serviceLabel.append("Serviço");
    const service = el("select", null, null);
    service.id = "live-att-service";
    serviceLabel.appendChild(service);
    wrap.append(personLabel, serviceSearchLabel, serviceLabel);

    const queue = el("button", null, "Confirmar atendimento e enfileirar");
    queue.type = "button";
    queue.id = "live-queue";
    wrap.appendChild(queue);

    serviceSearch.addEventListener("input", async () => {
      try {
        const items = await api.searchServices(serviceSearch.value);
        state.services = items;
        service.replaceChildren(...items.map((s) => option(s.id, s.name)));
      } catch (err) {
        reportError(err);
      }
    });

    queue.addEventListener("click", () => {
      if (!person.value || !service.value) {
        setFeedback("Selecione responsável e serviço antes de enfileirar.");
        return;
      }
      setFeedback("Atendimento registrado na fila da estação (fila local; a sincronização real com o relay é uma etapa posterior).");
    });

    refreshAttendance();
  }
})();
