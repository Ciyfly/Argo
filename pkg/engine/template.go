package engine

var ResultHtmlTemplate = `
<!DOCTYPE html>
<html>
  <head>
    <meta charset="UTF-8">
    <title>Argo result</title>
    <style>
      body {
        background-color: #262626;
        color: #f0f0f0;
        font-family: "Helvetica Neue", Helvetica, Arial, sans-serif;
        font-size: 14px;
        line-height: 1.5;
        margin: 0;
        padding: 0;
      }
      .container {
        max-width: 800px;
        padding: 20px;
      }
      h1 {
        font-size: 24px;
        margin: 0 0 20px;
      }
      table {
        border-collapse: collapse;
        width: 100%;
      }
      th, td {
        border: 1px solid #333;
        padding: 8px;
        text-align: left;
      }
      th {
        background-color: #333;
        color: #f0f0f0;
        font-weight: bold;
      }
    </style>
  </head>
  <body>
    <div class="container">
      <h1>Argo result</h1>
      <p>hostname：{{.HostName}}</p>
      <p>count：{{.Count}}</p>
      <p>date：{{.DateTime}}</p>
      <table>
        <thead>
          <tr>
			<th>Method</th>
            <th>URL</th>
            <th>Data</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
		{{range .ResultList}}
		<tr>
		<th>{{.Method}}</th>
		<th>{{.URL}}</th>
		<th>{{.Data}}</th>
		<th>{{.Status}}</th>
		</tr>
		{{end}}
        </tbody>
      </table>
    </div>
  </body>
</html>

`
