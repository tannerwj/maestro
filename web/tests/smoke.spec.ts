import { expect, test } from "@playwright/test";

test("operator smoke covers overview, workflow, and config studio", async ({ page }) => {
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Workflows" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Recent activity" })).toBeVisible();

  await page.getByRole("button", { name: /gitlab-platform/i }).first().click();
  await expect(page).toHaveURL(/\/workflows\/gitlab-platform$/);
  await expect(page.getByRole("link", { name: "platform/app#42" })).toBeVisible();
  await page.getByRole("button", { name: "Start" }).click();
  await expect(page.getByText(/operator guidance received/i)).toBeVisible();
  await expect(page.getByRole("heading", { name: "Live output" })).toBeVisible();

  await page.getByRole("button", { name: /settings/i }).click();
  await expect(page).toHaveURL(/\/settings$/);
  await page.getByRole("button", { name: "YAML", exact: true }).click();
  await page.getByRole("button", { name: "Validate" }).click();
  await expect(page.getByText("Config is valid.")).toBeVisible();

  await page.getByRole("button", { name: "Backups", exact: true }).click();
  await page.getByRole("button", { name: "Create backup" }).click();
  await expect(page.getByText(/maestro\.yaml\.bak\./).first()).toBeVisible();

  await page.getByRole("button", { name: /agent packs/i }).click();
  await expect(page).toHaveURL(/\/packs$/);
  await expect(page.getByRole("heading", { name: "Existing packs" })).toBeVisible();
});

test("cross-origin control request is rejected while same-origin request works", async ({ request }) => {
  const forged = await request.post("/api/v1/poll", {
    headers: { Origin: "https://attacker.invalid", "Content-Type": "application/x-www-form-urlencoded" },
  });
  expect(forged.status()).toBe(403);

  const local = await request.post("/api/v1/poll", {
    headers: { Origin: "http://127.0.0.1:8746" },
  });
  expect(local.status()).toBe(200);

  const rebound = await request.get("/api/v1/status", {
    headers: { Host: "attacker.invalid:8746" },
  });
  expect(rebound.status()).toBe(403);
});
