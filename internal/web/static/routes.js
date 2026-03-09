(() => {
  function setHidden(element, hidden) {
    if (!element) return;
    element.classList.toggle("is-hidden", hidden);
    if (hidden) {
      element.setAttribute("hidden", "hidden");
    } else {
      element.removeAttribute("hidden");
    }
  }

  function bindCopyButtons(root) {
    root.querySelectorAll(".copy-button").forEach((button) => {
      button.addEventListener("click", async () => {
        const text = button.getAttribute("data-copy-text") || "";
        if (!text) return;
        try {
          await navigator.clipboard.writeText(text);
          const original = button.textContent;
          button.textContent = "Copied";
          window.setTimeout(() => {
            button.textContent = original;
          }, 1200);
        } catch (_err) {
          button.textContent = "Copy failed";
          window.setTimeout(() => {
            button.textContent = "Copy URL";
          }, 1200);
        }
      });
    });
  }

  function ensureTargetRowHandlers(form) {
    form.querySelectorAll("[data-remove-target]").forEach((button) => {
      if (button.dataset.bound === "true") {
        return;
      }
      button.dataset.bound = "true";
      button.addEventListener("click", () => {
        const rows = form.querySelectorAll(".target-row");
        const row = button.closest(".target-row");
        if (!row) return;
        if (rows.length <= 1) {
          row.querySelectorAll("input").forEach((input) => {
            input.value = input.name === "target_weight" ? "1" : "";
          });
          return;
        }
        row.remove();
      });
    });
  }

  function initRouteForm(form) {
    const algorithmSelect = form.querySelector("[data-algorithm-select]");
    const fuzzyField = form.querySelector("[data-fuzzy-field]");
    const weightHeading = form.querySelector("[data-weight-heading]");
    const pendingToggle = form.querySelector("[data-pending-toggle]");
    const pendingFields = form.querySelector("[data-pending-fields]");
    const stickinessToggle = form.querySelector("[data-stickiness-toggle]");
    const stickinessFields = form.querySelector("[data-stickiness-fields]");
    const stickinessMode = form.querySelector("[data-stickiness-mode]");
    const stickinessParamField = form.querySelector("[data-stickiness-param-field]");
    const inspector = form.querySelector("[data-algorithm-inspector]");
    const label = form.querySelector("[data-algorithm-label]");
    const description = form.querySelector("[data-algorithm-description]");
    const useCase = form.querySelector("[data-algorithm-use-case]");
    const addTargetButton = form.querySelector("[data-add-target]");
    const targetRows = form.querySelector("[data-target-rows]");
    const template = form.querySelector("template[data-target-template]");

    function syncAlgorithmUI() {
      const selected = algorithmSelect.options[algorithmSelect.selectedIndex];
      const needsFuzzy = selected?.dataset.needsFuzzy === "true";
      const usesWeights = selected?.dataset.usesWeights === "true";
      setHidden(fuzzyField, !needsFuzzy);
      setHidden(weightHeading, !usesWeights);
      form.querySelectorAll("[data-target-weight]").forEach((input) => {
        input.closest(".target-row")?.classList.toggle("target-row-unweighted", !usesWeights);
        input.closest(".target-row")?.querySelector("[data-target-weight]")?.classList.toggle("is-hidden", !usesWeights);
        input.closest(".target-row")?.querySelector("[data-target-weight]")?.toggleAttribute("hidden", !usesWeights);
      });
      if (label) label.textContent = selected?.textContent || "";
      if (description) description.textContent = selected?.dataset.description || "";
      if (useCase) useCase.innerHTML = selected?.dataset.useCase ? `<strong>Use when:</strong> ${selected.dataset.useCase}` : "";
      if (inspector) {
        inspector.classList.toggle("algorithm-inspector-weighted", usesWeights);
      }
    }

    function syncPendingUI() {
      setHidden(pendingFields, !pendingToggle?.checked);
    }

    function syncStickinessUI() {
      const enabled = !!stickinessToggle?.checked;
      setHidden(stickinessFields, !enabled);
      setHidden(stickinessParamField, !enabled || stickinessMode?.value !== "query_param");
    }

    if (addTargetButton && targetRows && template) {
      addTargetButton.addEventListener("click", () => {
        const fragment = template.content.cloneNode(true);
        targetRows.appendChild(fragment);
        ensureTargetRowHandlers(form);
        syncAlgorithmUI();
      });
    }

    algorithmSelect?.addEventListener("change", syncAlgorithmUI);
    pendingToggle?.addEventListener("change", syncPendingUI);
    stickinessToggle?.addEventListener("change", syncStickinessUI);
    stickinessMode?.addEventListener("change", syncStickinessUI);

    ensureTargetRowHandlers(form);
    syncAlgorithmUI();
    syncPendingUI();
    syncStickinessUI();
  }

  document.addEventListener("DOMContentLoaded", () => {
    bindCopyButtons(document);
    document.querySelectorAll("[data-route-form]").forEach(initRouteForm);
  });
})();
