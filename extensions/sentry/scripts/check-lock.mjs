import { verifyPublicLock } from "./public-lock.mjs";
const result = await verifyPublicLock(process.argv[2], "../../sdk/ts");
console.log(`public lock: PASS (${result.packages} entries, ${result.publicResolved} public sources, ${result.localSDKLinks} relative SDK link)`);
