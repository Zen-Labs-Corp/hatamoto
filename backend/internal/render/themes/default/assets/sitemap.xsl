<?xml version="1.0" encoding="UTF-8"?>
<xsl:stylesheet version="1.0"
  xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
  xmlns:sitemap="http://www.sitemaps.org/schemas/sitemap/0.9"
  xmlns:xhtml="http://www.w3.org/1999/xhtml"
  exclude-result-prefixes="sitemap xhtml">

  <xsl:output method="html" encoding="UTF-8" indent="yes" />

  <xsl:template match="/">
    <html lang="en">
      <head>
        <meta charset="UTF-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1.0" />
        <title>
          <xsl:choose>
            <xsl:when test="sitemap:sitemapindex">Sitemap Index</xsl:when>
            <xsl:otherwise>Sitemap</xsl:otherwise>
          </xsl:choose>
        </title>
        <style>
          * { margin: 0; padding: 0; box-sizing: border-box; }
          body {
            font-family: 'Inter', system-ui, -apple-system, sans-serif;
            font-size: 13px;
            line-height: 1.5;
            color: #374151;
            background: #ffffff;
          }
          .header {
            background: #DC2626;
            padding: 20px 32px;
          }
          .header h1 {
            color: #ffffff;
            font-size: 16px;
            font-weight: 600;
            letter-spacing: 0.05em;
          }
          .header p {
            color: rgba(255, 255, 255, 0.85);
            font-size: 12px;
            margin-top: 4px;
          }
          .container {
            max-width: 1200px;
            margin: 0 auto;
            padding: 24px 32px;
          }
          .meta {
            display: flex;
            gap: 24px;
            margin-bottom: 20px;
            font-size: 12px;
            color: #6b7280;
            text-transform: uppercase;
            letter-spacing: 0.1em;
          }
          .meta span {
            font-weight: 500;
            color: #374151;
          }
          table {
            width: 100%;
            border-collapse: collapse;
            font-size: 13px;
          }
          thead th {
            text-align: left;
            padding: 10px 12px;
            font-size: 11px;
            font-weight: 600;
            text-transform: uppercase;
            letter-spacing: 0.1em;
            color: #6b7280;
            border-bottom: 2px solid #e5e7eb;
            background: #f9fafb;
          }
          tbody td {
            padding: 8px 12px;
            border-bottom: 1px solid #e5e7eb;
            vertical-align: top;
          }
          tbody tr:hover {
            background: #f9fafb;
          }
          a {
            color: #374151;
            text-decoration: none;
          }
          a:hover {
            color: #DC2626;
          }
          .url-cell {
            word-break: break-all;
            max-width: 600px;
          }
          .priority {
            font-variant-numeric: tabular-nums;
          }
          .changefreq, .lastmod {
            white-space: nowrap;
            color: #6b7280;
          }
          .alternates {
            font-size: 11px;
            color: #9ca3af;
            margin-top: 4px;
          }
          .alternates span {
            display: inline-block;
            background: #f3f4f6;
            border: 1px solid #e5e7eb;
            border-radius: 3px;
            padding: 1px 5px;
            margin-right: 4px;
            margin-top: 2px;
            font-size: 10px;
            text-transform: uppercase;
            letter-spacing: 0.05em;
          }
          .footer {
            margin-top: 32px;
            padding-top: 16px;
            border-top: 1px solid #e5e7eb;
            font-size: 11px;
            color: #9ca3af;
          }
        </style>
      </head>
      <body>
        <div class="header">
          <h1>
            <xsl:choose>
              <xsl:when test="sitemap:sitemapindex">XML Sitemap Index</xsl:when>
              <xsl:otherwise>XML Sitemap</xsl:otherwise>
            </xsl:choose>
          </h1>
          <p>This sitemap is generated for search engines. You can find more information about XML sitemaps at <a href="https://www.sitemaps.org/" style="color: rgba(255,255,255,0.9); text-decoration: underline;">sitemaps.org</a>.</p>
        </div>
        <div class="container">
          <xsl:choose>
            <xsl:when test="sitemap:sitemapindex">
              <xsl:call-template name="sitemapIndex" />
            </xsl:when>
            <xsl:otherwise>
              <xsl:call-template name="urlSet" />
            </xsl:otherwise>
          </xsl:choose>
          <div class="footer">
          </div>
        </div>
      </body>
    </html>
  </xsl:template>

  <!-- Sitemap Index template -->
  <xsl:template name="sitemapIndex">
    <div class="meta">
      <div>Sub-sitemaps: <span><xsl:value-of select="count(sitemap:sitemapindex/sitemap:sitemap)" /></span></div>
    </div>
    <table>
      <thead>
        <tr>
          <th style="width: 70%">Sitemap URL</th>
          <th style="width: 30%">Last Modified</th>
        </tr>
      </thead>
      <tbody>
        <xsl:for-each select="sitemap:sitemapindex/sitemap:sitemap">
          <tr>
            <td class="url-cell">
              <a href="{sitemap:loc}"><xsl:value-of select="sitemap:loc" /></a>
            </td>
            <td class="lastmod">
              <xsl:value-of select="sitemap:lastmod" />
            </td>
          </tr>
        </xsl:for-each>
      </tbody>
    </table>
  </xsl:template>

  <!-- URL Set template -->
  <xsl:template name="urlSet">
    <div class="meta">
      <div>URLs: <span><xsl:value-of select="count(sitemap:urlset/sitemap:url)" /></span></div>
    </div>
    <table>
      <thead>
        <tr>
          <th style="width: 50%">URL</th>
          <th style="width: 15%">Priority</th>
          <th style="width: 15%">Change Freq</th>
          <th style="width: 20%">Last Modified</th>
        </tr>
      </thead>
      <tbody>
        <xsl:for-each select="sitemap:urlset/sitemap:url">
          <tr>
            <td class="url-cell">
              <a href="{sitemap:loc}"><xsl:value-of select="sitemap:loc" /></a>
              <xsl:if test="xhtml:link">
                <div class="alternates">
                  <xsl:for-each select="xhtml:link[@rel='alternate']">
                    <span><xsl:value-of select="@hreflang" /></span>
                  </xsl:for-each>
                </div>
              </xsl:if>
            </td>
            <td class="priority">
              <xsl:value-of select="sitemap:priority" />
            </td>
            <td class="changefreq">
              <xsl:value-of select="sitemap:changefreq" />
            </td>
            <td class="lastmod">
              <xsl:value-of select="sitemap:lastmod" />
            </td>
          </tr>
        </xsl:for-each>
      </tbody>
    </table>
  </xsl:template>

</xsl:stylesheet>
