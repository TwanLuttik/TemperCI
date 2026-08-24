import { settingsFormPatch, settingsFormValue } from "./settings-form";

const form = { listen_addr: "127.0.0.1:8080" };

if (settingsFormValue(form, "mcp_token") !== "") {
  throw new Error("missing key should be empty string");
}

const next = settingsFormPatch(form, "mcp_token", "secret");
if (settingsFormValue(next, "mcp_token") !== "secret") {
  throw new Error("patch must store keys that were not on the initial form");
}
if (settingsFormValue(form, "mcp_token") !== "") {
  throw new Error("patch must not mutate the previous form");
}

console.log("settings-form.test.ts ok");
