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

  const state = { parties: [], deceased: [], services: [], legalEntities: [], attendanceRepresentative: null };

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

  function reportError(err, offlineMessage = "Sem conexão com o servidor. A alteração não foi enviada e pode ser repetida depois.") {
    if (api.isOfflineError(err)) {
      setConnection("Offline", "status-loading");
      setFeedback(offlineMessage);
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
    if (err.status === 409) {
      setFeedback("Conflito: " + (err.message || "o registro já existe."));
      return;
    }
    if (err.status === 503 && err.code === "PRIVACY_POLICY_UNAVAILABLE") {
      setFeedback("Gravação de dados pessoais indisponível: a política de privacidade do DPO ainda não foi aprovada.");
      return;
    }
    setFeedback("Erro: " + (err.message || "falha inesperada.") + (err.traceId ? " (rastreio " + err.traceId + ")" : ""));
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

    // Pessoa jurídica (issue #78): cadastro, contato, endereço e representante PF.
    wrap.appendChild(el("h2", null, "Pessoa jurídica"));
    wrap.appendChild(el("p", "muted", "CNPJ numérico ou alfanumérico. Use apenas dados sintéticos em homologação."));
    const labeled = (text, node) => {
      const label = el("label", null, null);
      label.append(text);
      label.appendChild(node);
      return label;
    };
    const textInput = (id, placeholder, max) => {
      const input = el("input", null, null);
      input.type = "text";
      input.id = id;
      input.maxLength = max;
      input.placeholder = placeholder;
      return input;
    };
    const leName = textInput("live-le-name", "Razão social", 200);
    const leCnpj = textInput("live-le-cnpj", "00.000.000/0000-00", 18);
    const leTrade = textInput("live-le-trade", "Nome fantasia (opcional)", 200);
    const leForm = el("div", "store-fields", null);
    leForm.append(labeled("Razão social", leName), labeled("CNPJ", leCnpj), labeled("Nome fantasia", leTrade));
    wrap.appendChild(leForm);
    const leCreate = el("button", null, "Cadastrar pessoa jurídica");
    leCreate.type = "button";
    leCreate.id = "live-le-create";
    wrap.appendChild(leCreate);

    const leSearch = el("input", null, null);
    leSearch.type = "search";
    leSearch.id = "live-le-search";
    leSearch.autocomplete = "off";
    leSearch.maxLength = 200;
    leSearch.placeholder = "Razão social, fantasia ou CNPJ";
    wrap.appendChild(labeled("Buscar pessoa jurídica", leSearch));
    const leEmpty = el("p", null, "Nenhuma pessoa jurídica encontrada.");
    leEmpty.id = "live-le-empty";
    leEmpty.hidden = true;
    leEmpty.setAttribute("role", "status");
    wrap.appendChild(leEmpty);
    const leSelect = el("select", null, null);
    leSelect.id = "live-le-select";
    wrap.appendChild(labeled("Pessoa jurídica selecionada", leSelect));
    const leDetail = el("ul", "store-list", null);
    leDetail.id = "live-le-detail";
    leDetail.setAttribute("aria-live", "polite");
    wrap.appendChild(leDetail);

    const contactKind = el("select", null, null);
    contactKind.id = "live-le-contact-kind";
    contactKind.replaceChildren(option("email", "E-mail"), option("phone", "Telefone"), option("whatsapp", "WhatsApp"), option("other", "Outro"));
    const contactValue = textInput("live-le-contact-value", "contato@exemplo.test", 254);
    const contactPrimary = el("input", null, null);
    contactPrimary.type = "checkbox";
    contactPrimary.id = "live-le-contact-primary";
    const contactForm = el("div", "store-fields", null);
    contactForm.append(labeled("Tipo de contato", contactKind), labeled("Valor", contactValue), labeled("Principal", contactPrimary));
    wrap.appendChild(contactForm);
    const contactAdd = el("button", null, "Adicionar contato");
    contactAdd.type = "button";
    contactAdd.id = "live-le-contact-add";
    wrap.appendChild(contactAdd);

    const addrStreet = textInput("live-le-addr-street", "Logradouro", 120);
    const addrNumber = textInput("live-le-addr-number", "Número", 120);
    const addrCity = textInput("live-le-addr-city", "Cidade", 120);
    const addrState = textInput("live-le-addr-state", "UF", 2);
    const addrZip = textInput("live-le-addr-zip", "00000-000", 9);
    const addrPrimary = el("input", null, null);
    addrPrimary.type = "checkbox";
    addrPrimary.id = "live-le-addr-primary";
    const addrForm = el("div", "store-fields", null);
    addrForm.append(labeled("Logradouro", addrStreet), labeled("Número", addrNumber), labeled("Cidade", addrCity),
      labeled("UF", addrState), labeled("CEP", addrZip), labeled("Principal", addrPrimary));
    wrap.appendChild(addrForm);
    const addrAdd = el("button", null, "Adicionar endereço");
    addrAdd.type = "button";
    addrAdd.id = "live-le-addr-add";
    wrap.appendChild(addrAdd);

    const repPerson = el("select", null, null);
    repPerson.id = "live-le-rep-person";
    const repRole = el("select", null, null);
    repRole.id = "live-le-rep-role";
    repRole.replaceChildren(
      option("legal_representative", "Representante legal"),
      option("administrator", "Administrador"),
      option("attorney", "Procurador"),
      option("contact", "Contato"),
    );
    const repForm = el("div", "store-fields", null);
    repForm.append(labeled("Pessoa física (representante)", repPerson), labeled("Papel", repRole));
    wrap.appendChild(repForm);
    const repAdd = el("button", null, "Vincular representante");
    repAdd.type = "button";
    repAdd.id = "live-le-rep-add";
    wrap.appendChild(repAdd);

    const roleLabels = { legal_representative: "Representante legal", administrator: "Administrador", attorney: "Procurador", contact: "Contato" };
    const kindLabels = { email: "E-mail", phone: "Telefone", whatsapp: "WhatsApp", other: "Outro" };
    let searchRevision = 0;
    let detailRevision = 0;
    const renderEntities = (selectedId, showEmpty = false) => {
      leSelect.replaceChildren(...state.legalEntities.map((e) => option(e.id, e.legalName + " — " + e.cnpj)));
      if (state.legalEntities.some((entity) => entity.id === selectedId)) leSelect.value = selectedId;
      leEmpty.hidden = !showEmpty || state.legalEntities.length > 0;
    };
    const renderDetail = (entity) => {
      const rows = [entity.legalName + " — CNPJ " + entity.cnpj + (entity.tradeName ? " (" + entity.tradeName + ")" : "")];
      entity.contacts.forEach((c) => rows.push("Contato " + (kindLabels[c.kind] || c.kind) + ": " + c.value + (c.primary ? " (principal)" : "")));
      entity.addresses.forEach((a) => rows.push("Endereço: " + a.street + ", " + a.number + " — " + a.city + "/" + a.state + " " + a.zip + (a.primary ? " (principal)" : "")));
      const details = rows.map((row) => el("li", null, row));
      for (const representative of entity.representatives) {
        const row = el("li", null, (roleLabels[representative.role] || representative.role) + ": " + representative.name);
        if (representative.role === "legal_representative") {
          const useInAttendance = el("button", null, "Selecionar " + representative.name + " no atendimento");
          useInAttendance.type = "button";
          useInAttendance.addEventListener("click", () => {
            state.attendanceRepresentative = { id: representative.personId, name: representative.name };
            refreshAttendance();
            window.location.hash = "atendimento";
          });
          row.appendChild(useInAttendance);
        }
        details.push(row);
      }
      leDetail.replaceChildren(...details);
    };
    const refreshDetail = async () => {
      const revision = ++detailRevision;
      const selectedId = leSelect.value;
      leDetail.replaceChildren();
      if (!selectedId) return;

      try {
        const entity = await api.getLegalEntity(selectedId);
        if (revision === detailRevision && leSelect.value === selectedId) renderDetail(entity);
      } catch (err) {
        if (revision !== detailRevision || leSelect.value !== selectedId) return;
        leDetail.replaceChildren(el("li", null, "Não foi possível carregar os detalhes da pessoa jurídica."));
        if (api.isOfflineError(err)) setConnection("Offline", "status-loading");
      }
    };

    const submit = (button, action) => async () => {
      if (button.disabled) return;
      button.disabled = true;
      try {
        await action();
      } catch (err) {
        reportError(err);
      } finally {
        button.disabled = false;
      }
    };

    const withSelected = (button, message, action) => submit(button, async () => {
      const selectedId = leSelect.value;
      if (!selectedId) {
        setFeedback("Selecione ou cadastre uma pessoa jurídica antes.");
        return;
      }
      await action(selectedId);
      setFeedback(message);
      if (leSelect.value === selectedId) await refreshDetail();
    });

    leCreate.addEventListener("click", submit(leCreate, async () => {
      const form = { legalName: leName.value, cnpj: leCnpj.value, tradeName: leTrade.value };
      if (!form.legalName.trim() || !form.cnpj.trim()) {
        setFeedback("Informe razão social e CNPJ.");
        return;
      }
      const searchAtSubmit = searchRevision;
      const detailAtSubmit = detailRevision;
      const created = await api.createLegalEntity({
        legalName: form.legalName.trim(), cnpj: form.cnpj.trim(), tradeName: form.tradeName.trim(),
      });
      if (leName.value === form.legalName) leName.value = "";
      if (leCnpj.value === form.cnpj) leCnpj.value = "";
      if (leTrade.value === form.tradeName) leTrade.value = "";
      setFeedback("Pessoa jurídica cadastrada.");

      if (searchAtSubmit === searchRevision && detailAtSubmit === detailRevision) {
        ++searchRevision;
        state.legalEntities = [created].concat(state.legalEntities.filter((e) => e.id !== created.id));
        renderEntities(created.id);
        await refreshDetail();
      }
    }));

    leSearch.addEventListener("input", async () => {
      const revision = ++searchRevision;
      const query = leSearch.value.trim();
      const selectedId = leSelect.value;
      state.legalEntities = [];
      renderEntities();
      void refreshDetail();
      setFeedback("");
      if (!query) return;

      try {
        const found = await api.searchLegalEntities(query);
        if (revision !== searchRevision) return;
        state.legalEntities = found;
        renderEntities(selectedId, true);
        await refreshDetail();
      } catch (err) {
        if (revision === searchRevision) reportError(err, "Sem conexão para buscar pessoas jurídicas.");
      }
    });
    leSelect.addEventListener("change", refreshDetail);

    contactAdd.addEventListener("click", withSelected(contactAdd, "Contato registrado.", (selectedId) =>
      api.addContact(selectedId, { kind: contactKind.value, value: contactValue.value.trim(), primary: contactPrimary.checked })));
    addrAdd.addEventListener("click", withSelected(addrAdd, "Endereço registrado.", (selectedId) =>
      api.addAddress(selectedId, {
        street: addrStreet.value.trim(), number: addrNumber.value.trim(), city: addrCity.value.trim(),
        state: addrState.value.trim().toUpperCase(), zip: addrZip.value.trim(), primary: addrPrimary.checked,
      })));
    repAdd.addEventListener("click", withSelected(repAdd, "Representante vinculado.", async (selectedId) => {
      if (!repPerson.value) {
        const err = new Error("Selecione uma pessoa física para representar.");
        err.status = 400;
        throw err;
      }
      await api.addRepresentative(selectedId, { personId: repPerson.value, role: repRole.value });
    }));

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
      repPerson.replaceChildren(...items.map((p) => option(p.id, p.name)));
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
    if (!person) return;
    const representative = state.attendanceRepresentative;
    const selectedId = representative?.id || person.value;
    const people = representative
      ? [representative, ...state.parties.filter((party) => party.id !== representative.id)]
      : state.parties;
    person.replaceChildren(...people.map((party) => option(party.id, party.name)));
    if (people.some((party) => party.id === selectedId)) person.value = selectedId;
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
    person.addEventListener("change", () => { state.attendanceRepresentative = null; });
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

    const queue = el("button", null, "Conferir seleção do atendimento");
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
        setFeedback("Selecione responsável e serviço.");
        return;
      }
      setFeedback("Responsável e serviço selecionados. A confirmação do atendimento é feita no fluxo de contrato.");
    });

    refreshAttendance();
  }
})();
