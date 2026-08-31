import { describe, expect, it } from "vitest";
import { modelProviderKeySchema } from "./schemas";

const base = { id: "k1", name: "dbx", models: ["*"], blacklisted_models: [], weight: 1 };
const workspace = { value: "https://dbc-1.cloud.databricks.com", ref: "" };
const secret = (value: string) => ({ value, ref: "" });

describe("modelProviderKeySchema, databricks", () => {
	it("accepts an OAuth M2M key whose _auth_type was lost to a form reset", () => {
		// The key form resets once the key resolves, wiping the UI-only discriminator.
		// A service principal is still a complete credential without it, so requiring a
		// personal access token here would block saving a valid key.
		const result = modelProviderKeySchema.safeParse({
			...base,
			databricks_key_config: {
				workspace_url: workspace,
				client_id: secret("client-id"),
				client_secret: secret("client-secret"),
			},
		});
		expect(result.success).toBe(true);
	});

	it("accepts an OAuth M2M key with the discriminator present", () => {
		const result = modelProviderKeySchema.safeParse({
			...base,
			databricks_key_config: {
				workspace_url: workspace,
				client_id: secret("client-id"),
				client_secret: secret("client-secret"),
				_auth_type: "oauth_m2m",
			},
		});
		expect(result.success).toBe(true);
	});

	it("accepts a personal access token key", () => {
		const result = modelProviderKeySchema.safeParse({
			...base,
			value: secret("dapi-token"),
			databricks_key_config: { workspace_url: workspace, _auth_type: "pat" },
		});
		expect(result.success).toBe(true);
	});

	it("rejects a key with neither a token nor a service principal", () => {
		const result = modelProviderKeySchema.safeParse({
			...base,
			databricks_key_config: { workspace_url: workspace },
		});
		expect(result.success).toBe(false);
	});

	it("rejects half a service principal", () => {
		const result = modelProviderKeySchema.safeParse({
			...base,
			databricks_key_config: { workspace_url: workspace, client_id: secret("client-id") },
		});
		expect(result.success).toBe(false);
	});

	it("rejects a missing workspace URL", () => {
		const result = modelProviderKeySchema.safeParse({
			...base,
			value: secret("dapi-token"),
			databricks_key_config: { workspace_url: secret("") },
		});
		expect(result.success).toBe(false);
	});
});
