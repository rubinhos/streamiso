import { render } from "preact";
import { PlayerController } from "./controller";
import { Player } from "./Player";
import "./player.css";

const ctl = new PlayerController();
render(<Player ctl={ctl} />, document.getElementById("app")!);
ctl.start();
