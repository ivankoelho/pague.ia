import { describe, expect, it } from "vitest";
import { date, dueText, money, month, monthToDate, statusLabel } from "./format";

describe("formatação", () => {
  it("formata moeda em reais", () => {
    expect(money(1497.8).replace(/\s/g, " ")).toBe("R$ 1.497,80");
    expect(money(null)).toBe("");
  });
  it("formata datas e competência", () => {
    expect(date("2026-11-15")).toBe("15/11/2026");
    expect(month("2026-10-01")).toBe("10/2026");
    expect(monthToDate("2026-10")).toBe("2026-10-01");
  });
  it("descreve prazo de vencimento", () => {
    expect(dueText(-3)).toBe("venceu há 3 dias");
    expect(dueText(0)).toBe("vence hoje");
    expect(dueText(1)).toBe("em 1 dia");
  });
  it("traduz status do workflow", () => {
    expect(statusLabel("FOR_PAYMENT")).toBe("No Contas a Pagar");
    expect(statusLabel("X")).toBe("X");
  });
});
