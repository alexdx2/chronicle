import { ServiceBusClient } from "@azure/service-bus";

const sb = new ServiceBusClient(conn);
const rq = sb.createReceiver("js-queue");
const rt = sb.createReceiver("js-topic", "js-sub", { receiveMode: "peekLock" });
const sender = sb.createSender("js-out");
// const old = sb.createSender("js-old");
const dyn = sb.createSender(name);
