(async () => {
  const back = [...document.querySelectorAll('button')].find(item => item.innerText.trim() === '返回列表');
  if (back) back.click();
  await new Promise(resolve => setTimeout(resolve, 500));
  return { clicked: Boolean(back), body: document.body.innerText.slice(0, 300) };
})()
