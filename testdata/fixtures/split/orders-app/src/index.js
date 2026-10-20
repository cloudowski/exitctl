const express = require("express");
const { getOrder } = require("./repo");

const app = express();

app.get("/orders/:id", async (req, res) => {
  res.json(await getOrder(req.params.id));
});

app.listen(8080);
